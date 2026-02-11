package main

import (
	"fmt"
	"log"

	"github.com/SomtoJF/iris-worker/initializers/sqldb"
)

func init() {
	err := sqldb.ConnectToSQLite()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	fmt.Println("Hello, World!")
}
