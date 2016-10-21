package sql

import (
	"fmt"
)

type Catalog struct {
	Databases []Database
}

func (c Catalog) Database(name string) (Database, error) {
	for _, db := range c.Databases {
		if db.Name() == name {
			return db, nil
		}
	}

	return nil, fmt.Errorf("database not found: %s", name)
}

func (c Catalog) Table(dbName string, tableName string) (PhysicalRelation, error) {
	db, err := c.Database(dbName)
	if err != nil {
		fmt.Printf("db not found: %s\n", dbName)
		return nil, err
	}

	tables := db.Relations()
	table, found := tables[tableName]
	if !found {
		fmt.Printf("table not found: %s.%s\n", dbName, tableName)
		return nil, fmt.Errorf("table not found: %s", tableName)
	}

	return table, nil
}
