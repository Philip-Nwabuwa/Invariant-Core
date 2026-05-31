package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// FileFingerprint hashes the given files in order into a single hex digest. It
// streams each file through the hash (O(1) memory), so it stays cheap even for
// large inputs. The digest identifies a reconciliation input pair so a re-run of
// the same inputs can be recognized and not double-counted (NS-406, AC-4).
func FileFingerprint(paths ...string) (string, error) {
	h := sha256.New()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return "", fmt.Errorf("fingerprint open %q: %w", p, err)
		}
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("fingerprint read %q: %w", p, err)
		}
		_ = f.Close()
		// Separator so concatenation is unambiguous across files.
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
