package runtime

import (
	"os"
	"strconv"
)

const (
	envPort = "COUPER_PORT"
	envXFH  = "COUPER_XFH"
)

func undateByENV(conf *HTTPConfig) error {
	if err := configurePortByEnv(conf); err != nil {
		return err
	}

	configureXfhByEnv(conf)

	return nil
}

func configurePortByEnv(conf *HTTPConfig) error {
	if p := os.Getenv(envPort); p != "" {
		port, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return err
		}

		conf.ListenPort = int(port)
	}

	return nil
}

func configureXfhByEnv(conf *HTTPConfig) {
	switch os.Getenv(envXFH) {
	case "true":
		conf.UseXFH = true
	case "false":
		conf.UseXFH = false
	}
}
