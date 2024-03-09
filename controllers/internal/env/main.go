package env

import (
	"github.com/codingconcepts/env"
)

type Env struct {
}

func GetEnvOrDie() *Env {
	var ev Env
	if err := env.Set(&ev); err != nil {
		panic(err)
	}
	return &ev
}
