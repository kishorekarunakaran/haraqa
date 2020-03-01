package logs_test

import (
	"log"
	"os"

	"github.com/haraqa/haraqa/internal/examples/logs"
)

func ExampleLogger() {
	logger := log.New(os.Stderr, "ERROR", log.LstdFlags)
	logErr, err := logs.NewLogger(logger, []byte("Errors"))
	if err != nil {
		panic(err)
	}
	// Close should be called to flush any messages before exiting
	defer logErr.Close()

	logErr.Println("Some log here")
	logErr.Println("Another log here")
}
