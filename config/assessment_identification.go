package config

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
)

const assessmentUserAgent = "ARTEX-Assessment/1.0"

var assessmentReference = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)

// AssessmentIdentification maps task IDs to optional public engagement references.
// An absent task ID opts out. An empty reference opts in with the base identifier.
type AssessmentIdentification map[string]string

func (a *AssessmentIdentification) UnmarshalJSON(data []byte) error {
	var entries map[string]*string
	if err := json.Unmarshal(data, &entries); err != nil {
		return errors.New("assessment_identification must be an object mapping task IDs to public engagement references")
	}
	if entries == nil {
		*a = nil
		return nil
	}
	references := make(AssessmentIdentification, len(entries))
	for taskID, reference := range entries {
		id, err := strconv.ParseInt(taskID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != taskID {
			return errors.New("assessment_identification task IDs must be positive integers written without a sign or leading zeroes")
		}
		if reference == nil || !assessmentReference.MatchString(*reference) {
			return errors.New("assessment_identification references must be empty strings or contain 1 to 64 characters from A-Z, a-z, 0-9, dot, underscore and hyphen")
		}
		references[taskID] = *reference
	}
	*a = references
	return nil
}

// UserAgent returns the identifier configured for taskID, or empty if it is not enabled.
func (a AssessmentIdentification) UserAgent(taskID string) string {
	reference, ok := a[taskID]
	if !ok {
		return ""
	}
	if reference == "" {
		return assessmentUserAgent
	}
	return assessmentUserAgent + " (engagement=" + reference + ")"
}
