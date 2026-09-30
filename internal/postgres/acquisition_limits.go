package postgres

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultAcquisitionAttemptsPerRound = 2
	defaultRetainedFailedOutputs       = 4
)

// AcquisitionLimits bounds distinct submissions and retained failed outputs.
// Existing round snapshots are never changed by configuration updates.
type AcquisitionLimits struct {
	AttemptsPerRound      int `json:"attempts_per_round"`
	RetainedFailedOutputs int `json:"retained_failed_outputs"`
}

func ParseAcquisitionLimits(attempts, retained string) (AcquisitionLimits, error) {
	limits := AcquisitionLimits{defaultAcquisitionAttemptsPerRound, defaultRetainedFailedOutputs}
	for _, field := range []struct {
		input  string
		target *int
		name   string
	}{
		{attempts, &limits.AttemptsPerRound, "ACQUISITION_ATTEMPTS_PER_ROUND"},
		{retained, &limits.RetainedFailedOutputs, "ACQUISITION_RETAINED_OUTPUT_LIMIT"},
	} {
		if strings.TrimSpace(field.input) == "" {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(field.input))
		if err != nil {
			return AcquisitionLimits{}, fmt.Errorf("%s must be an integer", field.name)
		}
		*field.target = value
	}
	return limits, limits.validate()
}

func (limits AcquisitionLimits) validate() error {
	if limits.AttemptsPerRound < 1 || limits.AttemptsPerRound > 6 {
		return errors.New("ACQUISITION_ATTEMPTS_PER_ROUND must be between 1 and 6")
	}
	if limits.RetainedFailedOutputs < 1 || limits.RetainedFailedOutputs > 12 {
		return errors.New("ACQUISITION_RETAINED_OUTPUT_LIMIT must be between 1 and 12")
	}
	return nil
}

// ConfigureAcquisitionLimits is called once at startup before any workers or
// handlers run. It grants no new round and queues no acquisition by itself.
func (store *Store) ConfigureAcquisitionLimits(limits AcquisitionLimits) error {
	if err := limits.validate(); err != nil {
		return err
	}
	store.acquisitionLimits = limits
	return nil
}

func (store *Store) AcquisitionLimits() AcquisitionLimits {
	limits := store.acquisitionLimits
	if limits.AttemptsPerRound == 0 {
		limits.AttemptsPerRound = defaultAcquisitionAttemptsPerRound
	}
	if limits.RetainedFailedOutputs == 0 {
		limits.RetainedFailedOutputs = defaultRetainedFailedOutputs
	}
	return limits
}
