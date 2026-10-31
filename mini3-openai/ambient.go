package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ambientLight reads a Linux IIO ambient-light input.
type ambientLight struct{ path string }

func findAmbientLight() ambientLight {
	if path := strings.TrimSpace(os.Getenv("MINI3_LIGHT_SENSOR")); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return ambientLight{path: path}
		}
		return ambientLight{}
	}
	for _, pattern := range []string{
		"/sys/bus/iio/devices/iio:device*/in_illuminance_input",
		"/sys/bus/iio/devices/iio:device*/in_intensity_input",
	} {
		paths, _ := filepath.Glob(pattern)
		if len(paths) > 0 {
			return ambientLight{path: paths[0]}
		}
	}
	return ambientLight{}
}

func (s ambientLight) readLux() (float64, bool) {
	if s.path == "" {
		return 0, false
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return 0, false
	}
	lux, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil || math.IsNaN(lux) || math.IsInf(lux, 0) || lux < 0 {
		return 0, false
	}
	return lux, true
}

// displayBrightness maps the sensor reading linearly from absolute level 5 at 0.2 to 255 at 4.
func displayBrightness(lux float64) int {
	if lux <= 0.2 {
		return 5
	}
	if lux >= 4 {
		return 255
	}
	return int(math.Round(5 + (lux-0.2)*250/3.8))
}

const displayBrightnessRampStep = 2

func approachBrightness(current, target, step int) int {
	if target > current+step {
		return current + step
	}
	if target < current-step {
		return current - step
	}
	return target
}
