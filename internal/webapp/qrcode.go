package webapp

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

// qrCodeSVGDataURI generates a pure-Go SVG QR code data URI containing the given text.
func qrCodeSVGDataURI(content string) (string, error) {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("generate QR code: %w", err)
	}
	bitmap := qr.Bitmap()
	size := len(bitmap)
	if size == 0 {
		return "", fmt.Errorf("empty QR code")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size)
	sb.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	sb.WriteString(`<path fill="#000000" d="`)
	for y, row := range bitmap {
		for x, val := range row {
			if val {
				fmt.Fprintf(&sb, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	sb.WriteString(`"/></svg>`)

	svg := sb.String()
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), nil
}

// confDataURI returns a client-side data URI suitable for downloading content as a .conf file.
func confDataURI(content string) string {
	return "data:text/plain;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(content))
}
