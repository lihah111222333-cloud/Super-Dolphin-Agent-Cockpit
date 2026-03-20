package shared

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func NewID(prefix string) string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}
