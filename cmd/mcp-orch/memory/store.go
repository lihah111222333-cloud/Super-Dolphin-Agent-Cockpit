package memory

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

const memoryHookMaxRunes = 150

type diskEntry struct {
	entry         contractMemoryEntry
	canonicalName string
	filePath      string
}

type indexEntry struct {
	name          string
	description   string
	memoryType    contractMemoryType
	path          string
	hook          string
	canonicalName string
}

type contractMemoryEntry = contract.MemoryEntry

type contractMemoryType = contract.MemoryType
