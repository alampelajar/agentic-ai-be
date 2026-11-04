package models

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// A struct tag with a missing closing quote is silently ignored by
// reflect.StructTag, so GORM would see no size, NOT NULL or default at all.
func TestPasswordResetChallengeTagsAreWellFormed(t *testing.T) {
	typ := reflect.TypeOf(PasswordResetChallenge{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "CreatedAt" || field.Name == "UpdatedAt" {
			continue // plain json tag only
		}
		if _, ok := field.Tag.Lookup("gorm"); !ok {
			t.Errorf("%s: gorm tag is missing or malformed: %q", field.Name, field.Tag)
		}
	}
}

func TestPasswordResetChallengeHashColumns(t *testing.T) {
	parsed, err := schema.Parse(&PasswordResetChallenge{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	// The OTP hash and the reset token hash are the same kind of column:
	// a SHA-256 hex digest that is never NULL and is empty when unused.
	for _, name := range []string{"OTPHash", "ResetTokenHash"} {
		field := parsed.LookUpField(name)
		if field == nil {
			t.Fatalf("%s is not a column", name)
		}
		if field.Size != 64 {
			t.Errorf("%s size = %d, want 64", name, field.Size)
		}
		if !field.NotNull {
			t.Errorf("%s must be NOT NULL", name)
		}
		// GORM strips the quotes: default:'' is a declared default of the empty string.
		if !field.HasDefaultValue || field.DefaultValue != "" || field.DefaultValueInterface != "" {
			t.Errorf("%s default = %q / %v (declared: %v), want an empty string", name, field.DefaultValue, field.DefaultValueInterface, field.HasDefaultValue)
		}
	}
	if got := parsed.LookUpField("OTPHash").DBName; got != "otp_hash" {
		t.Errorf("OTPHash column = %q, want otp_hash (handlers update it by that name)", got)
	}
}
