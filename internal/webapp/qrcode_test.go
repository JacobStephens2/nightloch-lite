package webapp

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/skip2/go-qrcode"
)

func TestQRCodeSVGDataURI(t *testing.T) {
	t.Parallel()

	content := "[Interface]\nPrivateKey = aaaa\nAddress = 10.66.0.2/32\nDNS = 10.66.0.1\n\n[Peer]\nPublicKey = bbbb\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = vpn.example.com:51820\n"

	dataURI, err := qrCodeSVGDataURI(content)
	if err != nil {
		t.Fatalf("qrCodeSVGDataURI: %v", err)
	}

	prefix := "data:image/svg+xml;base64,"
	if !strings.HasPrefix(dataURI, prefix) {
		t.Fatalf("dataURI does not start with %q: %s", prefix, dataURI[:min(len(dataURI), 35)])
	}

	raw := strings.TrimPrefix(dataURI, prefix)
	decodedBytes, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("failed to decode base64 data URI: %v", err)
	}
	svgContent := string(decodedBytes)

	if !strings.HasPrefix(svgContent, "<svg") || !strings.HasSuffix(strings.TrimSpace(svgContent), "</svg>") {
		t.Fatalf("svgContent is not a valid SVG: %s", svgContent)
	}

	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		t.Fatalf("qrcode.New: %v", err)
	}
	expectedBitmap := qr.Bitmap()
	expectedSize := len(expectedBitmap)

	wantViewBox := fmt.Sprintf(`viewBox="0 0 %d %d"`, expectedSize, expectedSize)
	if !strings.Contains(svgContent, wantViewBox) {
		t.Errorf("svg missing expected viewBox %q: %s", wantViewBox, svgContent[:min(len(svgContent), 120)])
	}
}

func TestConfDataURI(t *testing.T) {
	t.Parallel()

	content := "[Interface]\nPrivateKey = aaaa\nAddress = 10.66.0.2/32\n"
	dataURI := confDataURI(content)

	prefix := "data:text/plain;charset=utf-8;base64,"
	if !strings.HasPrefix(dataURI, prefix) {
		t.Fatalf("confDataURI does not start with %q: %s", prefix, dataURI)
	}

	raw := strings.TrimPrefix(dataURI, prefix)
	decodedBytes, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("failed to decode base64 conf data URI: %v", err)
	}

	if string(decodedBytes) != content {
		t.Fatalf("decoded conf = %q, want %q", string(decodedBytes), content)
	}
}
