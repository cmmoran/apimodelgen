package canonical

import "github.com/google/uuid"

type TestEmbedded struct {
	ID uuid.UUID `gorm:"primary_key" json:"id" yaml:"id" mapstructure:"id"`
}

type PrimaryKey interface {
	~string | ~[]byte |
		// smaller int primary key types can be used for enums with small id spaces
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		// short git commit hashes for example
		~[8]byte |
		// UUIDs, ULIDs, etc.
		~[16]byte |
		// sha256 hashes for example
		~[32]byte |
		// sha512 hashes for example
		~[64]byte
}

type TestEmbeddedGeneric[T PrimaryKey] struct {
	ID T `gorm:"primary_key" json:"id" yaml:"id" mapstructure:"id"`
}

type TestWidget struct {
	TestEmbedded `gorm:",embedded" mapstructure:",squash" json:",inline" yaml:",inline" dto:"-"`
	WodgetID     uuid.UUID `gorm:"type:uuid;" json:"wodget_id" yaml:"wodget_id" mapstructure:"wodget_id"`
	Name         string    `gorm:"type:text;" json:"name" yaml:"name" mapstructure:"name"`
	Category     int       `gorm:"type:numeric(2);" json:"age" yaml:"age" mapstructure:"age"`
}

type TestWidgets []*TestWidget

type TestWodget struct {
	TestEmbedded `gorm:",embedded" mapstructure:",squash" json:",inline" yaml:",inline" dto:"-"`
	Widgets      TestWidgets `gorm:"foreignkey:WodgetID" json:"widgets" yaml:"widgets" mapstructure:"widgets"`
}

type TestWodgets []TestWodget

type TestWadget struct {
	Ref uuid.UUID `gorm:"type:uuid;primaryKey" json:"ref" yaml:"ref" mapstructure:"ref"`
	Key string    `gorm:"primary_key" json:"key" yaml:"key" mapstructure:"key"`
	// DepField Deprecated this field will be removed in a subsequent release
	DepField string      `gorm:"type:text;" json:"dep_field" yaml:"dep_field" mapstructure:"dep_field"`
	WodgetID uuid.UUID   `gorm:"type:uuid;" json:"wodget_id" yaml:"wodget_id" mapstructure:"wodget_id"`
	Wodgets  TestWodgets `gorm:"foreignkey:WodgetID" json:"wodgets" yaml:"wodgets" mapstructure:"wodgets"`
}

// TestDeprecatedStruct
// Deprecated
type TestDeprecatedStruct struct {
	TestEmbedded `gorm:",embedded" mapstructure:",squash" json:",inline" yaml:",inline"  dto:"-"`
}

type TestWidgetGeneric struct {
	TestEmbeddedGeneric[uuid.UUID] `gorm:",embedded" mapstructure:",squash" json:",inline" yaml:",inline"`
	WidgetID                       uuid.UUID `json:"widget_id" mapstructure:"widget_id" yaml:"widget_id"`
}
