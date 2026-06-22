package datasourcev2

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
	"unicode"
)

const (
	datasourceV2ChunkTargetTokens  = 256
	datasourceV2ChunkMaxBytes      = 64 * 1024
	datasourceV2EmbeddingModel     = "local-token-hash-v1"
	datasourceV2EmbeddingDimension = 384
	datasourceV2EmbeddingBytes     = datasourceV2EmbeddingDimension * 4
)

// datasourceV2ChunkEmbedding 将 chunk 文本转换为 sqlite-vec 兼容的 float32 BLOB。
// 这里使用本地 token hashing，避免导入流程依赖外部服务；同一段文本会稳定得到同一向量。
func datasourceV2ChunkEmbedding(text string) ([]byte, int32, error) {
	vector := make([]float32, datasourceV2EmbeddingDimension)
	var tokenCount int32
	if err := forEachDatasourceV2Token(text, func(token string) error {
		if tokenCount == math.MaxInt32 {
			return errDatasourceV2TextTooLarge
		}
		tokenCount++
		addDatasourceV2TokenFeature(vector, token)
		return nil
	}); err != nil {
		return nil, 0, err
	}
	normalizeDatasourceV2Vector(vector)
	return serializeDatasourceV2Vector(vector), tokenCount, nil
}

// forEachDatasourceV2Token 复用导入切块的轻量 token 规则。
// ASCII 字母/数字/下划线连续段算一个 token，非空白的非 ASCII 字符逐 rune 计 token。
func forEachDatasourceV2Token(text string, visit func(string) error) error {
	var ascii strings.Builder
	flushASCII := func() error {
		if ascii.Len() == 0 {
			return nil
		}
		token := ascii.String()
		ascii.Reset()
		return visit(token)
	}
	for _, r := range text {
		if isDatasourceV2ASCIITokenRune(r) {
			ascii.WriteRune(lowerASCII(r))
			continue
		}
		if err := flushASCII(); err != nil {
			return err
		}
		if unicode.IsSpace(r) {
			continue
		}
		if err := visit(strings.ToLower(string(r))); err != nil {
			return err
		}
	}
	return flushASCII()
}

func addDatasourceV2TokenFeature(vector []float32, token string) {
	digest := sha256.Sum256([]byte(token))
	index := binary.LittleEndian.Uint64(digest[:8]) % uint64(len(vector))
	weight := float32(1)
	if digest[8]&1 == 1 {
		weight = -1
	}
	vector[index] += weight
}

func normalizeDatasourceV2Vector(vector []float32) {
	var sumSquares float64
	for _, value := range vector {
		sumSquares += float64(value * value)
	}
	if sumSquares == 0 {
		return
	}
	scale := float32(1 / math.Sqrt(sumSquares))
	for i := range vector {
		vector[i] *= scale
	}
}

func serializeDatasourceV2Vector(vector []float32) []byte {
	blob := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(value))
	}
	return blob
}

func isDatasourceV2ASCIITokenRune(r rune) bool {
	return r == '_' || '0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z'
}

func lowerASCII(r rune) rune {
	if 'A' <= r && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

func datasourceV2AdvanceChunkTokenState(r rune, asciiOpen bool) (bool, bool) {
	if isDatasourceV2ASCIITokenRune(r) {
		return !asciiOpen, true
	}
	if unicode.IsSpace(r) {
		return false, false
	}
	return true, false
}
