package storage

import (
	"errors"
	"os"
	"path/filepath"
)

type Pager struct {
    file *os.File
}

// NewPager creates a new pager object
func NewPager(dir, filename string) (*Pager, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	
	path := filepath.Join(dir, filename)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &Pager{file: file}, nil
}

// Close closes the file pager is holding
func (pg *Pager) Close() error {
    return pg.file.Close()
}

// WritePage writes a page to the given file at the specified pageID.
// It will overwrite the page if it exists, or expand the file if the pageID is beyond the current file size.
func (x *Pager) WritePage(pageID uint32, p *Page) error {
	if len(p.data) != PageSize {
		return errors.New("page data size is invalid")
	}

	offset := int64(pageID) * PageSize
	if _, err := x.file.WriteAt(p.data, offset); err != nil {
		return err
	}

	return nil
}

// ReadPage fetches a page from the given file at the specified pageID and loads it into the given Page pointer.
func (x *Pager) ReadPage(pageID uint32, p *Page) error {
	if len(p.data) != PageSize {
		return errors.New("page data size is invalid")
	}

	offset := int64(pageID) * PageSize
	if _, err := x.file.ReadAt(p.data, offset); err != nil {
		return err
	}

	return nil
}

// AllocatePage allocates a new page for the given file.
func (x *Pager) AllocatePage(mode uint8) (uint32, error) {
    info, err := x.file.Stat()
    if err != nil {
        return 0, err
    }
    newPageID := uint32(info.Size() / PageSize)
    p := NewPage(mode)
    p.SetPageID(newPageID)
    return newPageID, x.WritePage(newPageID, p)
}