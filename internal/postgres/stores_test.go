package postgres

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A subject embedded without a pool would pass the compiler and panic on its
// first query in production.
func TestNewStoreWiresEverySubject(t *testing.T) {
	pool := &pgxpool.Pool{}
	value := reflect.ValueOf(*newStore(pool))
	subjects := 0
	for index := range value.NumField() {
		field := value.Type().Field(index)
		if !field.Anonymous {
			continue
		}
		subjects++
		if got := value.Field(index).FieldByName("Pool"); !got.IsValid() || got.Pointer() != reflect.ValueOf(pool).Pointer() {
			t.Errorf("subject %s is not given the store's database pool", field.Name)
		}
	}
	if subjects == 0 {
		t.Fatal("no embedded subjects found")
	}
}
