package archive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	uuid "github.com/google/uuid"
	"github.com/joagonca/rmapi/log"
	"github.com/joagonca/rmapi/util"
	"github.com/nfnt/resize"
)

func makeThumbnail(pdf []byte) ([]byte, error) {
	// 1. Write PDF to temporary file (pdftoppm requires a file path)
	tmpPdf, err := os.CreateTemp("", "rmapi-pdf-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp PDF file: %w", err)
	}
	tmpPdfPath := tmpPdf.Name()
	defer os.Remove(tmpPdfPath)

	if _, err := tmpPdf.Write(pdf); err != nil {
		tmpPdf.Close()
		return nil, fmt.Errorf("failed to write temp PDF: %w", err)
	}
	tmpPdf.Close()

	// 2. Use pdftoppm to render first page to PNG
	// Output will be: tmpPdfPath.png (with -singlefile flag)
	tmpImgPath := tmpPdfPath + ".png"
	defer os.Remove(tmpImgPath)

	cmd := exec.Command("pdftoppm",
		"-png",          // Output as PNG
		"-singlefile",   // Single page only (no page number suffix)
		"-f", "1",       // First page
		"-l", "1",       // Last page (also first page)
		"-scale-to", "800", // Scale to reasonable resolution
		tmpPdfPath,
		tmpPdfPath) // Output prefix (will create tmpPdfPath.png)

	// Capture stderr for better error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w\nStderr: %s\nEnsure 'pdftoppm' is installed (part of poppler-utils)", err, stderr.String())
	}

	// 3. Load the rendered image
	imgFile, err := os.Open(tmpImgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open rendered image: %w", err)
	}
	defer imgFile.Close()

	img, err := png.Decode(imgFile)
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %w", err)
	}

	// 4. Resize to reMarkable thumbnail dimensions (280x374 pixels)
	thumbnail := resize.Resize(280, 374, img, resize.Lanczos3)

	// 5. Encode as JPEG
	out := &bytes.Buffer{}
	if err := jpeg.Encode(out, thumbnail, nil); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %w", err)
	}

	return out.Bytes(), nil
}

// GetIdFromZip tries to get the Document UUID from an archive
func GetIdFromZip(srcPath string) (id string, err error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		return
	}
	zip := Zip{}
	err = zip.Read(file, fi.Size())
	if err != nil {
		return
	}
	id = zip.UUID
	return
}

func CreateZipDocument(id, srcPath string) (zipPath string, err error) {
	_, ext := util.DocPathToName(srcPath)
	fileType := ext

	if ext == util.ZIP {
		zipPath = srcPath
		return
	}

	doc, err := ioutil.ReadFile(srcPath)
	if err != nil {
		log.Error.Println("failed to open source document file to read", err)
		return
	}
	// Create document (pdf or epub) file
	tmp, err := ioutil.TempFile("", "rmapizip")
	if err != nil {
		return
	}
	defer tmp.Close()

	if err != nil {
		log.Error.Println("failed to create tmpfile for zip doc", err)
		return
	}

	w := zip.NewWriter(tmp)
	defer w.Close()

	var documentPath string

	pages := make([]string, 0)
	if ext == util.RM {
		pageUUID := uuid.New()
		pageID := pageUUID.String()
		documentPath = fmt.Sprintf("%s/%s.rm", id, pageID)
		fileType = "notebook"
		pages = append(pages, pageID)
	} else {
		documentPath = fmt.Sprintf("%s.%s", id, ext)
		pages = append(pages, "")
	}

	f, err := w.Create(documentPath)
	if err != nil {
		log.Error.Println("failed to create doc entry in zip file", err)
		return
	}
	f.Write(doc)

	//try to create a thumbnail
	//thumbnail generation is opt-in via RMAPI_THUMBNAILS environment variable
	if ext == util.PDF && os.Getenv("RMAPI_THUMBNAILS") != "" {
		thumbnail, err := makeThumbnail(doc)
		if err != nil {
			log.Error.Println("cannot generate thumbnail", err)
		} else {
			f, err := w.Create(fmt.Sprintf("%s.thumbnails/0.jpg", id))
			if err != nil {
				log.Error.Println("failed to create doc entry in zip file", err)
				return "", err
			}
			f.Write(thumbnail)
		}
	}

	// Create pagedata file
	f, err = w.Create(fmt.Sprintf("%s.pagedata", id))
	if err != nil {
		log.Error.Println("failed to create content entry in zip file", err)
		return
	}
	f.Write(make([]byte, 0))

	// Create content content
	f, err = w.Create(fmt.Sprintf("%s.content", id))
	if err != nil {
		log.Error.Println("failed to create content entry in zip file", err)
		return
	}

	c, err := createZipContent(fileType, pages)
	if err != nil {
		return
	}

	f.Write([]byte(c))
	zipPath = tmp.Name()

	return
}

func CreateZipDirectory(id string) (string, error) {
	tmp, err := ioutil.TempFile("", "rmapizip")

	if err != nil {
		log.Error.Println("failed to create tmpfile for zip dir", err)
		return "", err
	}
	defer tmp.Close()

	w := zip.NewWriter(tmp)
	defer w.Close()

	// Create content content
	f, err := w.Create(fmt.Sprintf("%s.content", id))
	if err != nil {
		log.Error.Println("failed to create content entry in zip file", err)
		return "", err
	}

	f.Write([]byte("{}"))

	return tmp.Name(), nil
}

func createZipContent(ext string, pageIDs []string) (string, error) {
	c := Content{
		DummyDocument: false,
		ExtraMetadata: ExtraMetadata{
			LastPen:             "Finelinerv2",
			LastTool:            "Finelinerv2",
			LastFinelinerv2Size: "1",
		},
		FileType:       ext,
		PageCount:      0,
		LastOpenedPage: 0,
		LineHeight:     -1,
		Margins:        180,
		TextScale:      1,
		Transform: Transform{
			M11: 1,
			M12: 0,
			M13: 0,
			M21: 0,
			M22: 1,
			M23: 0,
			M31: 0,
			M32: 0,
			M33: 1,
		},
		Pages: pageIDs,
	}

	cstring, err := json.Marshal(c)

	if err != nil {
		log.Error.Println("failed to serialize content file", err)
		return "", err
	}

	return string(cstring), nil
}

func CreateContent(id, ext, fpath string, pageIds []string) (fileName, filePath string, err error) {
	fileName = id + ".content"
	filePath = path.Join(fpath, fileName)
	content := "{}"

	if ext != "" {
		content, err = createZipContent(ext, pageIds)
		if err != nil {
			return
		}
	}

	err = ioutil.WriteFile(filePath, []byte(content), 0600)
	return
}

func UnixTimestamp() string {
	t := time.Now().UnixNano() / 1000000
	tf := strconv.FormatInt(t, 10)
	return tf
}

func CreateMetadata(id, name, parent, colType, fpath string) (fileName string, filePath string, err error) {
	fileName = id + ".metadata"
	filePath = path.Join(fpath, fileName)
	meta := MetadataFile{
		DocName:        name,
		Version:        0,
		CollectionType: colType,
		Parent:         parent,
		Synced:         true,
		LastModified:   UnixTimestamp(),
	}

	c, err := json.Marshal(meta)
	if err != nil {
		return
	}

	err = ioutil.WriteFile(filePath, c, 0600)
	return
}
