package intercept

import "net/http"

// ApplyAssessmentUserAgent adds the task's configured identifier to a target
// request. An explicitly supplied User-Agent, including an empty one, takes
// precedence. It reports whether it inserted the identifier.
func ApplyAssessmentUserAgent(req *http.Request) bool {
	scope, ok := TargetScopeFromContext(req.Context())
	if !ok || scope.TaskID == "" || scope.ConversationID != 0 || scope.AssessmentUserAgent == "" {
		return false
	}
	if _, explicit := req.Header["User-Agent"]; explicit {
		return false
	}
	req.Header.Set("User-Agent", scope.AssessmentUserAgent)
	return true
}
