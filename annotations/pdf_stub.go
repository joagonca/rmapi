// +build !cairo

package annotations

import (
	"errors"
)

const (
	DeviceWidth  = 1404
	DeviceHeight = 1872
)

type PdfGenerator struct {
	zipName        string
	outputFilePath string
	options        PdfGeneratorOptions
}

type PdfGeneratorOptions struct {
	AddPageNumbers  bool
	AllPages        bool
	AnnotationsOnly bool
}

func CreatePdfGenerator(zipName, outputFilePath string, options PdfGeneratorOptions) *PdfGenerator {
	return &PdfGenerator{zipName: zipName, outputFilePath: outputFilePath, options: options}
}

func (p *PdfGenerator) Generate() error {
	return errors.New("PDF generation with annotations requires building with Cairo support. Build with: go build -tags cairo")
}
