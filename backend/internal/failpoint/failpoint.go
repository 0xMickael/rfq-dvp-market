// Package failpoint pauses at explicit crash boundaries in isolated integration tests.
package failpoint

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

var once sync.Once

func Hit(name string) {
	if os.Getenv("RFQ_TEST_FAILPOINT") != name || os.Getenv("RFQ_TEST_MODE") != "1" {
		return
	}
	once.Do(func() {
		dir := os.Getenv("RFQ_TEST_SIGNAL_DIR")
		if dir == "" {
			return
		}
		if os.WriteFile(filepath.Join(dir, name+".ready"), []byte("ready"), 0600) != nil {
			return
		}
		// The harness kills this real process; no production API can activate this hook.
		for {
			time.Sleep(time.Second)
		}
	})
}
