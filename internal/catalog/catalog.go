package catalog

import (
	"fmt"
	"sync"

	"gopostgres/internal/types"
)

type ColumnDef struct {
	Name       string
	TypeOID    types.OID
	TypeMod    int32
	NotNull    bool
	HasDefault bool
	Default    string
}

type TableDef struct {
	OID     uint32
	Name    string
	Schema  string
	Columns []*ColumnDef
}

type Catalog struct {
	mu      sync.RWMutex
	tables  map[string]*TableDef // key: "schema.table"
	nextOID uint32
}

func NewCatalog() *Catalog {
	return &Catalog{
		tables: make(map[string]*TableDef),
		// postgres reserves OIDs below that for system objects
		nextOID: 16384,
	}
}

func (c *Catalog) CreateTable(schema, name string, columns []*ColumnDef) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := schema + "." + name
	if _, exists := c.tables[key]; exists {
		return fmt.Errorf("relation %q already exists", name)
	}
	c.tables[key] = &TableDef{
		OID:     c.nextOID,
		Name:    name,
		Schema:  schema,
		Columns: columns,
	}
	c.nextOID++

	return nil
}

func (c *Catalog) DropTable(schema, name string, ifExists bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := schema + "." + name
	if _, exists := c.tables[key]; !exists {
		if ifExists {
			return nil
		}
		return fmt.Errorf("table %q does not exist", name)
	}
	delete(c.tables, key)

	return nil
}

func (c *Catalog) GetTable(schema, name string) (*TableDef, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := schema + "." + name
	table, exists := c.tables[key]
	if !exists {
		return nil, fmt.Errorf("relation %q does not exist", name)
	}

	return table, nil
}
