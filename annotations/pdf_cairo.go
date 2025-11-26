// +build cairo

package annotations

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/joagonca/rmapi/archive"
	rmencoding "github.com/joagonca/rmapi/encoding/rm"
	"github.com/joagonca/rmapi/log"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/ungerik/go-cairo"
)

/*
#cgo pkg-config: cairo
#include <stdlib.h>
#include <cairo.h>
#include <cairo-pdf.h>
*/
import "C"

const (
	DeviceWidth  = 1404
	DeviceHeight = 1872
)

// rmPageSize is the default page size for blank templates (in PDF points: 1/72 inch)
var rmPageSize = struct{ Width, Height float64 }{445, 594}

type PdfGenerator struct {
	zipName        string
	outputFilePath string
	options        PdfGeneratorOptions
	backgroundPDF  []byte
	template       bool
}

type PdfGeneratorOptions struct {
	AddPageNumbers  bool
	AllPages        bool
	AnnotationsOnly bool //export the annotations without the background/pdf
}

func CreatePdfGenerator(zipName, outputFilePath string, options PdfGeneratorOptions) *PdfGenerator {
	return &PdfGenerator{zipName: zipName, outputFilePath: outputFilePath, options: options}
}

func normalized(p1 rmencoding.Point, scale float64) (float64, float64) {
	return float64(p1.X) * scale, float64(p1.Y) * scale
}

// setPDFPageSize sets the size for the current page in a PDF surface
func setPDFPageSize(surface *cairo.Surface, width, height float64) {
	surfacePtr, _ := surface.Native()
	C.cairo_pdf_surface_set_size((*C.cairo_surface_t)(unsafe.Pointer(surfacePtr)), C.double(width), C.double(height))
}

func (p *PdfGenerator) Generate() error {
	file, err := os.Open(p.zipName)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	zip := archive.NewZip()

	fi, err := file.Stat()
	if err != nil {
		return err
	}

	err = zip.Read(file, fi.Size())
	if err != nil {
		return err
	}

	if zip.Content.FileType == "epub" {
		return errors.New("only pdf and notebooks supported")
	}

	if err = p.initBackgroundPages(zip.Payload); err != nil {
		return err
	}

	if len(zip.Pages) == 0 {
		return errors.New("the document has no pages")
	}

	// If we have a background PDF and not annotations-only mode, we need a two-step process
	if p.backgroundPDF != nil && !p.options.AnnotationsOnly {
		return p.generateWithBackground(zip)
	}

	// Otherwise, simple case: just annotations or blank pages
	return p.generateAnnotationsOnly(zip)
}

func (p *PdfGenerator) generateAnnotationsOnly(zip *archive.Zip) error {
	// Create a temporary file for PDF output (Cairo requires a file path)
	tmpFile, err := os.CreateTemp("", "rmapi-annotations-*.pdf")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Determine first page dimensions
	var firstWidth, firstHeight float64
	if p.template {
		firstWidth, firstHeight = rmPageSize.Width, rmPageSize.Height
	} else {
		// TODO: Get dimensions from background PDF
		firstWidth, firstHeight = rmPageSize.Width, rmPageSize.Height
	}

	// Create PDF surface
	pdfSurface := cairo.NewPDFSurface(tmpPath, firstWidth, firstHeight, cairo.PDF_VERSION_1_5)
	defer pdfSurface.Finish()

	pageCount := 0
	for _, pageAnnotations := range zip.Pages {
		hasContent := pageAnnotations.Data != nil

		// Skip pages without content unless AllPages is set
		if !p.options.AllPages && !hasContent {
			continue
		}

		pageCount++

		// Set page size (for pages after the first)
		if pageCount > 1 {
			var pageWidth, pageHeight float64
			if p.template {
				pageWidth, pageHeight = rmPageSize.Width, rmPageSize.Height
			} else {
				// TODO: Get dimensions from background PDF page
				pageWidth, pageHeight = rmPageSize.Width, rmPageSize.Height
			}
			setPDFPageSize(pdfSurface, pageWidth, pageHeight)
		}

		// Calculate scale
		pageWidth := firstWidth
		pageHeight := firstHeight
		ratio := pageHeight / pageWidth

		var scale float64
		if ratio < 1.33 {
			scale = pageWidth / DeviceWidth
		} else {
			scale = pageHeight / DeviceHeight
		}

		// Draw annotations if present
		if hasContent {
			if err := p.drawAnnotations(pdfSurface, pageAnnotations.Data, scale, pageHeight); err != nil {
				return err
			}
		}

		// Add page numbers if requested
		if p.options.AddPageNumbers {
			p.drawPageNumber(pdfSurface, pageCount, pageWidth, pageHeight)
		}

		// Show page (prepare for next page)
		if pageCount < len(zip.Pages) || p.options.AllPages {
			pdfSurface.ShowPage()
		}
	}

	pdfSurface.Finish()

	// Copy temp file to final destination
	return copyFile(tmpPath, p.outputFilePath)
}

func (p *PdfGenerator) generateWithBackground(zip *archive.Zip) error {
	// Step 1: Create annotations-only PDF with transparent background
	tmpAnnotations, err := os.CreateTemp("", "rmapi-annotations-*.pdf")
	if err != nil {
		return fmt.Errorf("failed to create temp annotations file: %w", err)
	}
	tmpAnnotationsPath := tmpAnnotations.Name()
	tmpAnnotations.Close()
	defer os.Remove(tmpAnnotationsPath)

	// Generate annotations PDF
	if err := p.generateAnnotationsOnly(zip); err != nil {
		return err
	}

	// Step 2: Write background PDF to temp file
	tmpBackground, err := os.CreateTemp("", "rmapi-background-*.pdf")
	if err != nil {
		return fmt.Errorf("failed to create temp background file: %w", err)
	}
	tmpBackgroundPath := tmpBackground.Name()
	if _, err := tmpBackground.Write(p.backgroundPDF); err != nil {
		tmpBackground.Close()
		os.Remove(tmpBackgroundPath)
		return fmt.Errorf("failed to write background PDF: %w", err)
	}
	tmpBackground.Close()
	defer os.Remove(tmpBackgroundPath)

	// Step 3: Merge background and annotations using pdfcpu
	outFile, err := os.Create(p.outputFilePath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Open both PDFs as ReadSeekers
	bgFile, err := os.Open(tmpBackgroundPath)
	if err != nil {
		return fmt.Errorf("failed to open background PDF: %w", err)
	}
	defer bgFile.Close()

	annFile, err := os.Open(tmpAnnotationsPath)
	if err != nil {
		return fmt.Errorf("failed to open annotations PDF: %w", err)
	}
	defer annFile.Close()

	// Merge: background first, then overlay annotations
	conf := model.NewDefaultConfiguration()
	rsc := []io.ReadSeeker{bgFile, annFile}
	if err := api.MergeRaw(rsc, outFile, false, conf); err != nil {
		return fmt.Errorf("failed to merge PDFs: %w", err)
	}

	return nil
}

func (p *PdfGenerator) drawAnnotations(surface *cairo.Surface, rmData *rmencoding.Rm, scale, pageHeight float64) error {
	surface.Save()
	defer surface.Restore()

	for _, layer := range rmData.Layers {
		for _, line := range layer.Lines {
			if len(line.Points) < 1 {
				continue
			}
			if line.BrushType == rmencoding.Eraser {
				continue
			}

			if line.BrushType == rmencoding.HighlighterV5 {
				// Draw highlighter as semi-transparent rectangle
				p.drawHighlighter(surface, line, scale, pageHeight)
			} else {
				// Draw regular stroke
				p.drawStroke(surface, line, scale, pageHeight)
			}
		}
	}

	return nil
}

func (p *PdfGenerator) drawHighlighter(surface *cairo.Surface, line rmencoding.Line, scale, pageHeight float64) {
	if len(line.Points) < 2 {
		return
	}

	last := len(line.Points) - 1
	x1, y1 := normalized(line.Points[0], scale)
	x2, _ := normalized(line.Points[last], scale)

	// Highlighter width
	width := scale * 30
	y1 += width / 2

	// Convert Y coordinate (Cairo origin is top-left, PDF is bottom-left)
	y := pageHeight - y1

	// Yellow color with 50% opacity
	surface.SetSourceRGBA(1.0, 1.0, 0.0, 0.5)
	surface.SetLineWidth(width)
	surface.SetLineCap(cairo.LINE_CAP_BUTT)

	surface.MoveTo(x1, y)
	surface.LineTo(x2, y)
	surface.Stroke()
}

func (p *PdfGenerator) drawStroke(surface *cairo.Surface, line rmencoding.Line, scale, pageHeight float64) {
	if len(line.Points) < 1 {
		return
	}

	// Set stroke color
	var r, g, b float64
	switch line.BrushColor {
	case rmencoding.Black:
		r, g, b = 0.0, 0.0, 0.0
	case rmencoding.White:
		r, g, b = 1.0, 1.0, 1.0
	case rmencoding.Grey:
		r, g, b = 0.5, 0.5, 0.5
	default:
		r, g, b = 0.0, 0.0, 0.0
	}
	surface.SetSourceRGB(r, g, b)

	// Set stroke width
	// Formula from original: line.BrushSize*6.0 - 10.8
	strokeWidth := float64(line.BrushSize)*6.0 - 10.8
	if strokeWidth < 0.5 {
		strokeWidth = 0.5
	}
	surface.SetLineWidth(strokeWidth)

	// Set line cap
	surface.SetLineCap(cairo.LINE_CAP_ROUND)
	surface.SetLineJoin(cairo.LINE_JOIN_ROUND)

	// Draw path
	for i, point := range line.Points {
		x, y := normalized(point, scale)
		// Convert Y coordinate
		y = pageHeight - y

		if i == 0 {
			surface.MoveTo(x, y)
		} else {
			surface.LineTo(x, y)
		}
	}

	surface.Stroke()
}

func (p *PdfGenerator) drawPageNumber(surface *cairo.Surface, pageNum int, pageWidth, pageHeight float64) {
	surface.Save()
	defer surface.Restore()

	surface.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
	surface.SetFontSize(8.0)
	surface.SetSourceRGB(0, 0, 0)

	text := fmt.Sprintf("%d", pageNum)
	surface.MoveTo(pageWidth-20, pageHeight-10)
	surface.ShowText(text)
}

func (p *PdfGenerator) initBackgroundPages(pdfArr []byte) error {
	if len(pdfArr) > 0 {
		// Check if PDF is encrypted and decrypt if necessary
		rs := bytes.NewReader(pdfArr)
		ctx, err := api.ReadContext(rs, model.NewDefaultConfiguration())
		if err != nil {
			return fmt.Errorf("failed to read PDF: %w", err)
		}

		// Check if encrypted by checking if Encrypt field exists
		if ctx.XRefTable.Encrypt != nil {
			log.Info.Println("PDF is encrypted - pdfcpu will handle decryption")
			// pdfcpu's ReadContext already handles decryption with empty password
		}

		p.backgroundPDF = pdfArr
		p.template = false
		return nil
	}

	p.template = true
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
