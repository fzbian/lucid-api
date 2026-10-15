package models

import (
	"sync"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestNominaPaymentNotesMySQLMigrationType(t *testing.T) {
	db, err := gorm.Open(mysql.New(mysql.Config{SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := schema.Parse(&NominaPayment{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	// Check the actual MySQL column definition used by AutoMigrate without a live DB.
	columnType := db.Migrator().FullDataTypeOf(model.LookUpField("Notes")).SQL
	if columnType != "longtext" {
		t.Fatalf("nomina_payments.notes migration type = %q, want longtext", columnType)
	}
}
