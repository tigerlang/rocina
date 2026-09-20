package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"rocina/internal/session"
)

func messagesPath(s *session.Session) string {
	return filepath.Join(s.DataDir, "messages.json")
}

func loadChats(s *session.Session) map[string][]Block {
	chats := map[string][]Block{}
	if s == nil || s.DataDir == "" {
		return chats
	}
	data, err := os.ReadFile(messagesPath(s))
	if err != nil {
		return chats
	}
	if err := json.Unmarshal(data, &chats); err != nil {
		return map[string][]Block{}
	}
	for agent, blocks := range chats {
		for index := range blocks {
			blocks[index].Done = true
		}
		chats[agent] = blocks
	}
	return chats
}

func saveChats(s *session.Session, chats map[string][]Block) {
	if s == nil || s.DataDir == "" {
		return
	}
	data, err := json.MarshalIndent(chats, "", "  ")
	if err != nil {
		return
	}
	path := messagesPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
