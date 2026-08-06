package config

import "testing"

func TestDatabaseConnectionChanged(t *testing.T) {
	base := DatabaseConfig{
		Type:     "postgres",
		Host:     "localhost",
		Port:     5432,
		User:     "edub",
		Password: "edub",
		Database: "edub",
		SSLMode:  "disable",
	}

	cases := []struct {
		name string
		old  DatabaseConfig
		new  DatabaseConfig
		want bool
	}{
		{name: "identical", old: base, new: base, want: false},
		{name: "host changed", old: base, new: withField(base, func(c *DatabaseConfig) { c.Host = "10.0.0.1" }), want: true},
		{name: "port changed", old: base, new: withField(base, func(c *DatabaseConfig) { c.Port = 5433 }), want: true},
		{name: "user changed", old: base, new: withField(base, func(c *DatabaseConfig) { c.User = "admin" }), want: true},
		{name: "password changed", old: base, new: withField(base, func(c *DatabaseConfig) { c.Password = "secret" }), want: true},
		{name: "database changed", old: base, new: withField(base, func(c *DatabaseConfig) { c.Database = "edub2" }), want: true},
		{name: "sslmode changed", old: base, new: withField(base, func(c *DatabaseConfig) { c.SSLMode = "require" }), want: true},
		{name: "both use dsn identical", old: DatabaseConfig{DSN: "postgres://a/b"}, new: DatabaseConfig{DSN: "postgres://a/b"}, want: false},
		{name: "both use dsn different", old: DatabaseConfig{DSN: "postgres://a/b"}, new: DatabaseConfig{DSN: "postgres://a/c"}, want: true},
		{name: "old dsn new fields", old: DatabaseConfig{DSN: "postgres://a/b"}, new: base, want: true},
		{name: "old fields new dsn", old: base, new: DatabaseConfig{DSN: "postgres://a/b"}, want: true},
		{name: "dsn equal despite field differences", old: DatabaseConfig{DSN: "postgres://a/b", Host: "x"}, new: DatabaseConfig{DSN: "postgres://a/b", Host: "y"}, want: false},
		{name: "dsn mode vs equivalent fields", old: DatabaseConfig{DSN: "postgres://edub:edub@localhost:5432/edub?sslmode=disable"}, new: base, want: false},
		{name: "dsn mode save round trip keeps dsn", old: DatabaseConfig{DSN: "postgres://edub:edub@localhost:5432/edub?sslmode=disable"}, new: DatabaseConfig{DSN: "postgres://edub:edub@localhost:5432/edub?sslmode=disable"}, want: false},
		{name: "dsn mode vs different database", old: DatabaseConfig{DSN: "postgres://edub:edub@localhost:5432/edub?sslmode=disable"}, new: DatabaseConfig{DSN: "postgres://edub:edub@localhost:5432/other?sslmode=disable"}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DatabaseConnectionChanged(tc.old, tc.new); got != tc.want {
				t.Fatalf("DatabaseConnectionChanged(%+v, %+v) = %v, want %v", tc.old, tc.new, got, tc.want)
			}
		})
	}
}

func withField(c DatabaseConfig, set func(*DatabaseConfig)) DatabaseConfig {
	set(&c)
	return c
}
