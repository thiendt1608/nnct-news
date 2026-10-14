package news

import "encore.dev/storage/sqldb"

var db = sqldb.NewDatabase("news", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})
