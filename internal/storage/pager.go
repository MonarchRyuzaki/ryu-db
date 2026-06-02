package storage

import (
	"errors"
	"os"
)

// WritePage writes a page to the given file at the specified pageID.
// It will overwrite the page if it exists, or expand the file if the pageID is beyond the current file size.
func WritePage(file *os.File, pageID uint32, p *Page) error {
	if len(p.data) != PageSize {
		return errors.New("page data size is invalid")
	}

	offset := int64(pageID) * PageSize
	if _, err := file.WriteAt(p.data, offset); err != nil {
		return err
	}

	return nil
}

// ReadPage fetches a page from the given file at the specified pageID and loads it into the given Page pointer.
func ReadPage(file *os.File, pageID uint32, p *Page) error {
	if len(p.data) != PageSize {
		return errors.New("page data size is invalid")
	}

	offset := int64(pageID) * PageSize
	if _, err := file.ReadAt(p.data, offset); err != nil {
		return err
	}

	return nil
}

// AllocatePage allocates a new page for the given file.
func AllocatePage(file *os.File, mode uint8) (uint32, error) {
    info, err := file.Stat()
    if err != nil {
        return 0, err
    }
    newPageID := uint32(info.Size() / PageSize)
    p := NewPage(mode)
    return newPageID, WritePage(file, newPageID, p)
}