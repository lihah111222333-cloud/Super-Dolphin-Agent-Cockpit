// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
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

// addDatasourceV2TokenFeature 将 token 的 hash 特征值累加到固定维度向量。
// 正负权重来自 hash 位，保证相同 token 稳定落点，也降低常见词单向偏移。
func addDatasourceV2TokenFeature(vector []float32, token string) {
	digest := sha256.Sum256([]byte(token))
	index := binary.LittleEndian.Uint64(digest[:8]) % uint64(len(vector))
	weight := float32(1)
	if digest[8]&1 == 1 {
		weight = -1
	}
	vector[index] += weight
}

// normalizeDatasourceV2Vector 对向量做 L2 归一化。
// 空文本或全空白文本会形成全零向量，此时保持零值，让调用方继续用 tokenCount 判断内容质量。
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

// serializeDatasourceV2Vector 将 float32 向量序列化为 sqlite-vec 兼容的 little-endian BLOB。
func serializeDatasourceV2Vector(vector []float32) []byte {
	blob := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(value))
	}
	return blob
}

// isDatasourceV2ASCIITokenRune 定义 datasource_v2 的 ASCII token 边界。
// 只把字母、数字和下划线合并为连续 token，避免把路径分隔符或标点并进检索词。
func isDatasourceV2ASCIITokenRune(r rune) bool {
	return r == '_' || '0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z'
}

// lowerASCII 只规整 ASCII 大写字母，非 ASCII 字符保持原样交给外层 unicode 规则。
func lowerASCII(r rune) rune {
	if 'A' <= r && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// datasourceV2AdvanceChunkTokenState 更新切块器的 ASCII token 状态。
// tokenStarted 表示本 rune 开启一个新 token；asciiOpen 表示下一 rune 仍可能属于同一 ASCII token。
func datasourceV2AdvanceChunkTokenState(r rune, asciiOpen bool) (bool, bool) {
	if isDatasourceV2ASCIITokenRune(r) {
		return !asciiOpen, true
	}
	if unicode.IsSpace(r) {
		return false, false
	}
	return true, false
}
