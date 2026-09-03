// Package sharedstore 提供壳级 localStorage 共享层：内存快照 + 落盘
// DSH_HOME/storages/shared-localstorage.json（原子写）。作为 wails service
// 绑定（application.Service），页面的 bridge 经 js-bridge（window.wails
// Call.ByName）把 localStorage 读写转接到这里——与页面 origin 无关，跨启动
// 延续。
//
// 桌面单实例：无并发写者，写即覆盖，无 CAS、无轮询。
package sharedstore

import (
	"bytes"
	"encoding/json"
	"log"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

const file = "storages/shared-localstorage.json"

// Store 是 localStorage 共享层的状态与落盘句柄。
type Store struct {
	file string

	mu       sync.Mutex
	state    map[string]string
	order    []string
	saveTail chan struct{} // 串行写盘队列信号（持锁读写）
}

// New 创建 Store（不读盘；Load 由调用方在挂载前调用）。
func New(home string) *Store {
	return &Store{
		file:  filepath.Join(home, file),
		state: map[string]string{},
	}
}

// Load 读取持久化快照（缺失/损坏时保持空快照）。
func (s *Store) Load() {
	raw, err := os.ReadFile(s.file)
	if err != nil {
		return
	}
	var parsed struct {
		State map[string]string `json:"state"`
		Order []string          `json:"order"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.State == nil {
		log.Printf("shared-store: 忽略损坏的 %s", s.file)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	valid := parsed.Order[:0]
	for _, k := range parsed.Order {
		if _, ok := parsed.State[k]; ok {
			valid = append(valid, k)
		}
	}
	s.state = parsed.State
	s.order = valid
}

// Get 读取一个 key；ok 为 false 表示不存在。
func (s *Store) Get(k string) (v string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok = s.state[k]
	return
}

// Set 写入一个 key（覆盖）。
func (s *Store) Set(k, v string) {
	s.mu.Lock()
	if _, exists := s.state[k]; !exists {
		s.order = append(s.order, k)
	}
	s.state[k] = v
	s.mu.Unlock()
	s.persist()
}

// Remove 删除一个 key。
func (s *Store) Remove(k string) {
	s.mu.Lock()
	delete(s.state, k)
	for i, key := range s.order {
		if key == k {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	s.persist()
}

// Clear 清空全部。
func (s *Store) Clear() {
	s.mu.Lock()
	s.state = map[string]string{}
	s.order = nil
	s.mu.Unlock()
	s.persist()
}

// Snapshot 返回当前状态与 key 顺序的副本（网关注入 bridge 种子时用：
// 页面启动即可同步读到上次会话写入的值，不依赖异步 IPC）。
func (s *Store) Snapshot() (map[string]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := make(map[string]string, len(s.state))
	maps.Copy(state, s.state)
	order := append([]string(nil), s.order...)
	return state, order
}

// Flush 等待当前写盘队列排空（测试与退出前调用，保证落盘完成）。
func (s *Store) Flush() {
	s.mu.Lock()
	tail := s.saveTail
	s.mu.Unlock()
	if tail != nil {
		<-tail
	}
}

// persist 把当前快照排入串行写盘队列（原子写：tmp + rename）。
func (s *Store) persist() {
	s.mu.Lock()
	raw := s.marshalLocked()
	next := make(chan struct{})
	prev := s.saveTail
	if prev == nil {
		prev = make(chan struct{})
		close(prev)
	}
	s.saveTail = next
	s.mu.Unlock()

	go func() {
		<-prev
		defer close(next)
		dir := filepath.Dir(s.file)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("shared-store: mkdir %s: %v", dir, err)
			return
		}
		tmp := s.file + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o644); err != nil {
			log.Printf("shared-store: write tmp: %v", err)
			return
		}
		if err := os.Rename(tmp, s.file); err != nil {
			log.Printf("shared-store: rename: %v", err)
		}
	}()
}

// marshalLocked 序列化当前快照（持锁调用）。
func (s *Store) marshalLocked() []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(`"state":{`)
	first := true
	for _, k := range s.order {
		v, ok := s.state[k]
		if !ok {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyJSON, _ := json.Marshal(k)
		valJSON, _ := json.Marshal(v)
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(valJSON)
	}
	buf.WriteString(`},"order":`)
	orderJSON, _ := json.Marshal(s.order)
	buf.Write(orderJSON)
	buf.WriteByte('}')
	return buf.Bytes()
}
