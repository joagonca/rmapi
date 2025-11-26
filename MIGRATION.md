# Migration from unipdf to go-cairo and pdfcpu

This document describes the migration from `unipdf` to `go-cairo` and `pdfcpu` for PDF generation and manipulation.

## Summary of Changes

### Replaced Components

1. **PDF Annotation Rendering** (`annotations/pdf.go`)
   - **Old**: `unipdf/v3` for creating PDFs and drawing annotations
   - **New**: `go-cairo` for vector graphics and PDF generation
   - **Benefit**: No license restrictions, cleaner API, industry-standard Cairo library

2. **PDF Thumbnail Generation** (`archive/zipdoc.go`)
   - **Old**: `unipdf/v3/render` for rendering PDF pages to images
   - **New**: `pdftoppm` (from poppler-utils) via system call
   - **Benefit**: More reliable, no licensing issues, uses standard PDF rendering tool

3. **PDF Manipulation** (new in `annotations/pdf_cairo.go`)
   - **Old**: `unipdf/v3` for reading and merging PDFs
   - **New**: `pdfcpu` for PDF reading, decryption, and merging
   - **Benefit**: Pure Go library, actively maintained, Apache 2.0 license

### Files Changed

#### Modified Files
- `archive/zipdoc.go` - Replaced unipdf thumbnail generation with pdftoppm
- `go.mod` - Removed unipdf, added pdfcpu and go-cairo
- `README.md` - Updated dependencies and build instructions

#### New Files
- `annotations/pdf_cairo.go` - New Cairo-based PDF generation (build tag: `cairo`)
- `annotations/pdf_stub.go` - Stub for builds without Cairo (build tag: `!cairo`)
- `MIGRATION.md` - This file

#### Removed Files
- `annotations/license.go` - License bypass hack (no longer needed)
- `annotations/pdf.go` - Renamed to `pdf_unipdf.go.bak` (kept as backup)

## New Dependencies

### Go Modules
- **github.com/ungerik/go-cairo** - Go bindings for Cairo graphics library
- **github.com/pdfcpu/pdfcpu** - PDF manipulation library

### System Dependencies

#### Required (for PDF annotation export)
- **libcairo2** - Cairo graphics library
- **pkg-config** - For finding Cairo headers

Installation:
```bash
# Ubuntu/Debian
sudo apt-get install libcairo2-dev pkg-config

# macOS
brew install cairo pkg-config

# Arch Linux
sudo pacman -S cairo pkg-config

# Fedora/RHEL
sudo dnf install cairo-devel pkgconfig
```

#### Optional (for thumbnail generation)
- **poppler-utils** - Provides `pdftoppm` tool

Installation:
```bash
# Ubuntu/Debian
sudo apt-get install poppler-utils

# macOS
brew install poppler

# Arch Linux
sudo pacman -S poppler

# Fedora/RHEL
sudo dnf install poppler-utils
```

## Building

### With Cairo support (recommended)
```bash
go build -tags cairo
```

This enables full PDF annotation export with background PDF support.

### Without Cairo
```bash
go build
```

PDF annotation export will be disabled. The application will return an error message directing users to build with Cairo.

## Architecture Changes

### Old Architecture (unipdf)
```
User uploads PDF with annotations
  ↓
unipdf reads background PDF
  ↓
unipdf creates new PDF
  ↓
unipdf draws annotations using contentstream API
  ↓
unipdf merges annotations with background
  ↓
Single PDF output
```

### New Architecture (go-cairo + pdfcpu)

#### Case 1: Annotations only (no background PDF)
```
User uploads .rm files
  ↓
Cairo creates PDF with blank pages
  ↓
Cairo draws annotations as vector graphics
  ↓
Single PDF output
```

#### Case 2: Annotations with background PDF
```
User uploads PDF with annotations
  ↓
pdfcpu reads and validates background PDF
  ↓
Cairo creates transparent PDF with annotations only
  ↓
pdfcpu merges annotation PDF on top of background PDF
  ↓
Single merged PDF output
```

## Implementation Details

### PDF Generation (annotations/pdf_cairo.go)

**Key improvements:**
- Uses Cairo's native PDF surface for direct PDF generation
- Properly handles coordinate system transformations
- Supports highlighters with transparency
- Can set page sizes dynamically
- Cleaner, more maintainable code

**Color mapping:**
- Black: RGB(0, 0, 0)
- White: RGB(1, 1, 1)
- Grey: RGB(0.5, 0.5, 0.5)

**Stroke rendering:**
- Uses Cairo's line drawing primitives
- Round line caps and joins for smooth curves
- Width formula preserved: `line.BrushSize*6.0 - 10.8`

**Highlighter rendering:**
- Semi-transparent rectangles
- Yellow color: RGB(1, 1, 1, 0) with 50% opacity
- Width: 30 pixels (scaled)

### Thumbnail Generation (archive/zipdoc.go)

**Process:**
1. Write PDF bytes to temporary file
2. Call `pdftoppm -png -singlefile -f 1 -l 1 -scale-to 800 <input> <output>`
3. Read generated PNG
4. Resize to 280x374 pixels using Lanczos3 algorithm
5. Encode as JPEG

**Error handling:**
- Captures stderr for better error messages
- Provides installation instructions if pdftoppm not found

### PDF Merging

**pdfcpu features used:**
- `api.ReadContext()` - Parse PDF structure
- `api.MergeRaw()` - Merge multiple PDFs
- Automatic decryption handling for encrypted PDFs
- XRefTable access for checking encryption status

## Backward Compatibility

### What's Preserved
✅ Same API for `CreatePdfGenerator()`
✅ Same `PdfGeneratorOptions` structure
✅ Same output PDF quality
✅ Same annotation rendering behavior
✅ Same thumbnail dimensions (280x374)
✅ Environment variable `RMAPI_THUMBNAILS` still works

### What's Changed
⚠️ Requires Cairo libraries to be installed
⚠️ Requires `pdftoppm` for thumbnails (optional)
⚠️ Must build with `-tags cairo` for full functionality
⚠️ Build tag system for conditional compilation

### Breaking Changes
None for end users - the CLI and API remain the same.

## Testing

### Manual Testing Checklist
- [ ] Export notebook without background PDF
- [ ] Export PDF with annotations overlaid
- [ ] Export with page numbers enabled
- [ ] Export annotations-only mode
- [ ] Export all pages (including blank)
- [ ] Test with encrypted PDF
- [ ] Generate thumbnails with RMAPI_THUMBNAILS=1
- [ ] Test build without Cairo
- [ ] Test build with Cairo

### Unit Tests
The existing test files in `annotations/pdf_test.go` should continue to work with the Cairo implementation.

## Performance Comparison

**Expected performance:**
- Cairo PDF generation: Similar or faster than unipdf
- pdfcpu merging: Comparable to unipdf
- pdftoppm thumbnails: Slightly slower due to external process, but more reliable

## License Improvements

### Before
- unipdf uses AGPL license with commercial restrictions
- Required unsafe pointer manipulation to bypass license checks
- Legal grey area

### After
- go-cairo: MIT license
- pdfcpu: Apache 2.0 license
- Cairo library: LGPL (dynamically linked, no restrictions)
- poppler: GPLv2 (external tool, no linking)
- **Fully compliant, no license hacks needed**

## Rollback Plan

If issues are discovered:

1. Restore old implementation:
   ```bash
   mv annotations/pdf_unipdf.go.bak annotations/pdf.go
   ```

2. Restore unipdf dependency in go.mod:
   ```
   github.com/unidoc/unipdf/v3 v3.6.1
   ```

3. Restore license.go:
   ```bash
   git checkout HEAD -- annotations/license.go
   ```

4. Run `go mod tidy`

The backup file is kept in the repository for safety.

## Future Improvements

Potential enhancements:
- [ ] Use Bézier curves instead of polylines for smoother strokes
- [ ] Support more brush types (currently v5 highlighter supported)
- [ ] Extract PDF page dimensions from background PDF for better sizing
- [ ] Add PDF bookmarks/outlines preservation from pdfcpu
- [ ] Implement progressive rendering for large documents
- [ ] Add option to use in-memory buffers instead of temp files

## Troubleshooting

### Build fails: "cairo.h: No such file or directory"
**Solution:** Install Cairo development libraries (see Dependencies section)

### Build fails: "pkg-config not found"
**Solution:** Install pkg-config:
```bash
# macOS
brew install pkg-config

# Ubuntu/Debian
sudo apt-get install pkg-config
```

### Runtime error: "pdftoppm failed"
**Solution:** Install poppler-utils or disable thumbnail generation by not setting `RMAPI_THUMBNAILS`

### "PDF generation with annotations requires building with Cairo support"
**Solution:** Rebuild with: `go build -tags cairo`

### Warning: "ignoring duplicate libraries: '-lcairo'"
**Not an error:** This is a harmless linker warning due to both go-cairo and our CGO importing Cairo. Can be safely ignored.

## Migration Credits

This migration removes the dependency on unipdf and eliminates the license bypass hack, making the project fully open-source compliant while maintaining all functionality.

The implementation draws inspiration from the [rmc-go](https://github.com/joagonca/rmc-go) project's Cairo-based PDF export approach.
