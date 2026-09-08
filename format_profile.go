package accdb

// FormatProfile defines the characteristics and byte layout parameters
// for a specific Jet/ACE database engine version.
type FormatProfile struct {
	Name              string
	Version           JetVersion
	PageSize          int
	MaxColumns        int
	TableDefPageType  byte
	DataPageType      byte
	LeafIndexPageType byte
	DataPageHeaderLen int
	RowCountOffset    int
	ColDefSize        int
	IndexSlotSize     int
	IndexDefSize      int
}

// Predefined profiles for different Jet/ACE versions
var (
	ProfileJet3 = FormatProfile{
		Name:              "Jet3",
		Version:           JetVersion3,
		PageSize:          2048,
		MaxColumns:        255,
		TableDefPageType:  0x02,
		DataPageType:      0x01,
		LeafIndexPageType: 0x04,
		DataPageHeaderLen: 8,
		RowCountOffset:    8,
		ColDefSize:        18,
		IndexSlotSize:     8,
		IndexDefSize:      12,
	}

	ProfileJet4 = FormatProfile{
		Name:              "Jet4",
		Version:           JetVersion4,
		PageSize:          4096,
		MaxColumns:        255,
		TableDefPageType:  0x02,
		DataPageType:      0x01,
		LeafIndexPageType: 0x04,
		DataPageHeaderLen: 14,
		RowCountOffset:    12,
		ColDefSize:        25,
		IndexSlotSize:     12,
		IndexDefSize:      12,
	}

	ProfileACE12 = FormatProfile{
		Name:              "ACE12",
		Version:           JetVersion5,
		PageSize:          4096,
		MaxColumns:        255,
		TableDefPageType:  0x02,
		DataPageType:      0x01,
		LeafIndexPageType: 0x04,
		DataPageHeaderLen: 14,
		RowCountOffset:    12,
		ColDefSize:        25,
		IndexSlotSize:     12,
		IndexDefSize:      12,
	}

	ProfileACE14 = FormatProfile{
		Name:              "ACE14",
		Version:           JetVersion5,
		PageSize:          4096,
		MaxColumns:        255,
		TableDefPageType:  0x02,
		DataPageType:      0x01,
		LeafIndexPageType: 0x04,
		DataPageHeaderLen: 14,
		RowCountOffset:    12,
		ColDefSize:        25,
		IndexSlotSize:     12,
		IndexDefSize:      12,
	}
)

// GetFormatProfile returns the FormatProfile matching the given JetVersion
func GetFormatProfile(v JetVersion) FormatProfile {
	switch v {
	case JetVersion3:
		return ProfileJet3
	case JetVersion4:
		return ProfileJet4
	case JetVersion5:
		return ProfileACE14
	default:
		return ProfileACE14
	}
}
