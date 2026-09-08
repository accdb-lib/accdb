package accdb

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzOpen(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 2048))
	f.Add(MagicACCDB)

	f.Fuzz(func(t *testing.T, data []byte) {
		tmp := filepath.Join(t.TempDir(), "fuzz.accdb")
		if err := os.WriteFile(tmp, data, 0600); err != nil {
			return
		}

		db, err := Open(tmp)
		if err == nil && db != nil {
			_ = db.Tables()
			_ = db.AllTables()
			_ = db.Version()
			_ = db.IsEncrypted()
			_ = db.Close()
		}
	})
}

func FuzzParseHeader(f *testing.F) {
	f.Add(make([]byte, 2048))
	f.Add(append(MagicACCDB, make([]byte, 2044)...))

	f.Fuzz(func(t *testing.T, data []byte) {
		db := &Database{
			data:   data,
			tables: make(map[string]*Table),
		}
		_ = db.parseHeader()
	})
}

func FuzzDecodeRow(f *testing.F) {
	f.Add(make([]byte, 512))
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00})
	f.Add(make([]byte, 14))
	f.Add(make([]byte, 16))
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x0e, 0x00})
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05, 0x00, 0x00, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		table := &Table{
			Name: "FuzzTable",
			Columns: []*Column{
				{Name: "ID", Type: ColTypeLongInt, ID: 1, Index: 0, Offset: 0, Length: 4},
				{Name: "Name", Type: ColTypeText, ID: 2, Index: 1, Offset: 0, Length: 50},
			},
			db: &Database{
				pageSize: 4096,
				data:     make([]byte, 8192),
			},
		}
		iter := &RowIterator{
			table:     table,
			pageIndex: 0,
			rowIndex:  0,
		}
		_, _ = iter.parseRowFromPage(data)
	})
}
