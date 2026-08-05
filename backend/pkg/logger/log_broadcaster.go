package logger

import (
	"encoding/json"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

// LogEntry 表示一条日志条目，用于 SSE 推送
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Caller  string `json:"caller"`
	Message string `json:"message"`
	Fields  string `json:"fields,omitempty"`
}

// Broadcaster 日志广播器，将日志条目推送给所有订阅的客户端
type Broadcaster struct {
	clients map[chan LogEntry]struct{}
	mu      sync.RWMutex
	maxSize int // 每个客户端缓冲区大小
}

// 全局广播器实例
var GlobalBroadcaster = NewBroadcaster(100)

// NewBroadcaster 创建新的广播器
func NewBroadcaster(bufferSize int) *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan LogEntry]struct{}),
		maxSize: bufferSize,
	}
}

// Subscribe 订阅日志流，返回接收日志的通道
func (b *Broadcaster) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, b.maxSize)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅
func (b *Broadcaster) Unsubscribe(ch chan LogEntry) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

// Broadcast 广播日志条目给所有订阅者
func (b *Broadcaster) Broadcast(entry LogEntry) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.clients {
		select {
		case ch <- entry:
		default:
			// 缓冲区满，丢弃旧日志（非阻塞）
		}
	}
}

// ClientCount 返回当前订阅客户端数量
func (b *Broadcaster) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// SSECore 自定义 zapcore.Core，将日志发送到 Broadcaster
type SSECore struct {
	zapcore.LevelEnabler
	broadcaster *Broadcaster
	fields      []zapcore.Field
}

// NewSSECore 创建 SSE 日志核心
func NewSSECore(broadcaster *Broadcaster, level zapcore.LevelEnabler) zapcore.Core {
	return &SSECore{
		LevelEnabler: level,
		broadcaster:  broadcaster,
		fields:       nil,
	}
}

func (c *SSECore) With(fields []zapcore.Field) zapcore.Core {
	clone := &SSECore{
		LevelEnabler: c.LevelEnabler,
		broadcaster:  c.broadcaster,
		fields:       make([]zapcore.Field, len(c.fields)+len(fields)),
	}
	copy(clone.fields, c.fields)
	copy(clone.fields[len(c.fields):], fields)
	return clone
}

func (c *SSECore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *SSECore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// 如果没有客户端订阅，直接返回
	if c.broadcaster.ClientCount() == 0 {
		return nil
	}

	// 合并字段
	allFields := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	allFields = append(allFields, c.fields...)
	allFields = append(allFields, fields...)

	// 序列化额外字段
	var fieldsJSON string
	if len(allFields) > 0 {
		enc := zapcore.NewMapObjectEncoder()
		for _, f := range allFields {
			f.AddTo(enc)
		}
		if data, err := json.Marshal(enc.Fields); err == nil {
			fieldsJSON = string(data)
		}
	}

	logEntry := LogEntry{
		Time:    entry.Time.Format(time.RFC3339),
		Level:   entry.Level.String(),
		Caller:  entry.Caller.TrimmedPath(),
		Message: entry.Message,
		Fields:  fieldsJSON,
	}

	c.broadcaster.Broadcast(logEntry)
	return nil
}

func (c *SSECore) Sync() error {
	return nil
}
