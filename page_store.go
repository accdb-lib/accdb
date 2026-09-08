package accdb

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrPageOutOfBounds = errors.New("page number out of bounds")
	ErrPageTooSmall    = errors.New("page size is too small")
	ErrCorruptDatabase = errors.New("corrupt database structure")
)

// PageStore defines the interface for reading, writing, and allocating pages
type PageStore interface {
	PageSize() int
	PageCount() uint32
	ReadPage(pageNumber uint32) ([]byte, error)
	WritePage(pageNumber uint32, data []byte) error
	AllocatePage(pageType PageType) (uint32, []byte, error)
	Bytes() []byte
	SetBytes(data []byte) error
	Close() error
}

// MemoryPageStore provides an in-memory page store implementation
type MemoryPageStore struct {
	mu       sync.RWMutex
	pageSize int
	data     []byte
	readOnly bool
}

// NewMemoryPageStore creates a new MemoryPageStore with specified page size and initial data
func NewMemoryPageStore(pageSize int, initialData []byte) (*MemoryPageStore, error) {
	if pageSize <= 0 {
		return nil, ErrPageTooSmall
	}
	store := &MemoryPageStore{
		pageSize: pageSize,
		data:     initialData,
	}
	return store, nil
}

func (s *MemoryPageStore) SetReadOnly(ro bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readOnly = ro
}

func (s *MemoryPageStore) IsReadOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readOnly
}

func (s *MemoryPageStore) PageSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pageSize
}

func (s *MemoryPageStore) PageCount() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pageSize == 0 {
		return 0
	}
	return uint32(len(s.data) / s.pageSize)
}

func (s *MemoryPageStore) ReadPage(pageNumber uint32) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	start, end, err := pageBounds(pageNumber, s.pageSize, len(s.data))
	if err != nil {
		return nil, fmt.Errorf("%w: page %d (%v)", ErrPageOutOfBounds, pageNumber, err)
	}

	// Return a copy to prevent external mutation
	page := make([]byte, s.pageSize)
	copy(page, s.data[start:end])
	return page, nil
}

func (s *MemoryPageStore) WritePage(pageNumber uint32, pageData []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.readOnly {
		return ErrReadOnly
	}

	if len(pageData) != s.pageSize {
		return fmt.Errorf("invalid page data size: expected %d, got %d", s.pageSize, len(pageData))
	}

	start, end, err := pageBounds(pageNumber, s.pageSize, len(s.data))
	if err != nil {
		return fmt.Errorf("%w: page %d (%v)", ErrPageOutOfBounds, pageNumber, err)
	}

	copy(s.data[start:end], pageData)
	return nil
}

func (s *MemoryPageStore) AllocatePage(pageType PageType) (uint32, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.readOnly {
		return 0, nil, ErrReadOnly
	}

	newPageNum := uint32(len(s.data) / s.pageSize)
	newPage := make([]byte, s.pageSize)
	newPage[0] = byte(pageType)

	s.data = append(s.data, newPage...)
	ret := make([]byte, s.pageSize)
	copy(ret, newPage)
	return newPageNum, ret, nil
}

func (s *MemoryPageStore) Bytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make([]byte, len(s.data))
	copy(copied, s.data)
	return copied
}

func (s *MemoryPageStore) SetBytes(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.readOnly {
		return ErrReadOnly
	}

	s.data = make([]byte, len(data))
	copy(s.data, data)
	return nil
}

func (s *MemoryPageStore) Close() error {
	return nil
}

// requireRange validates that an offset and length fit within the data boundary safely
func requireRange(data []byte, offset, length int) error {
	if offset < 0 || length < 0 || offset > len(data)-length {
		return ErrCorruptDatabase
	}
	return nil
}
