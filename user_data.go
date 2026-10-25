package nemau

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxUserDataBytes bounds the cloud-init document of an instance create request
// (CreateInstanceRequest.UserData) and of an instance group launch template
// (LaunchTemplate.UserData), counted in bytes after a leading byte order mark
// is removed. The platform enforces the same limit; the SDK checks it first so
// an oversized document never leaves the process. Neither document is returned
// by the platform: an instance group view exposes only user_data_present and
// user_data_sha256.
const MaxUserDataBytes = 16 * 1024

// utf8BOM is the byte order mark the platform strips before it checks a
// document, so the SDK strips it too before applying the same rules.
const utf8BOM = "\uFEFF"

// validateUserData applies the platform's rules to a cloud-init document: at
// most MaxUserDataBytes, valid UTF-8, no NUL byte, not blank, and a first line
// that is "#cloud-config", a "#!" script header, or a MIME multipart header.
//
// These are comfort checks: they refuse a document the API would refuse anyway,
// without a round trip. The document itself is never included in the error.
func validateUserData(document string) error {
	document = strings.TrimPrefix(document, utf8BOM)
	if len(document) > MaxUserDataBytes {
		return fmt.Errorf("nemau: user data must not exceed %d bytes", MaxUserDataBytes)
	}
	if !utf8.ValidString(document) {
		return fmt.Errorf("nemau: user data must be valid UTF-8")
	}
	if strings.IndexByte(document, 0) >= 0 {
		return fmt.Errorf("nemau: user data must not contain NUL bytes")
	}
	if strings.TrimSpace(document) == "" {
		return fmt.Errorf("nemau: user data must not be empty")
	}
	firstLine, _, _ := strings.Cut(document, "\n")
	firstLine = strings.TrimRight(firstLine, " \t\r")
	switch {
	case firstLine == "#cloud-config", strings.HasPrefix(firstLine, "#!"):
		return nil
	case strings.HasPrefix(strings.ToLower(firstLine), "content-type: multipart/"):
		return nil
	}
	return fmt.Errorf("nemau: user data must start with #cloud-config, #! or a MIME multipart header")
}
