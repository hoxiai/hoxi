package runner

import (
	"encoding/json"
	"strings"

	"github.com/hoxiai/hoxi/internal/dataprofiles"
)

// DataProfileSafetyChecker optionally verifies whether a named profile is marked
// ReadOnly in the host dataprofiles store.
type DataProfileSafetyChecker func(profileID string) (kind string, readOnly bool, exists bool)

// SetDataProfileSafetyChecker attaches a read-only profile metadata lookup callback.
func (s *Service) SetDataProfileSafetyChecker(fn DataProfileSafetyChecker) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.dataProfileChecker = fn
	s.mu.Unlock()
}

func isReadOnlyDataSQLQuery(query string) (bool, string) {
	return dataprofiles.IsReadOnlySQL(query)
}

func isReadOnlyDataSSHCommand(command string) (bool, string) {
	return dataprofiles.IsReadOnlySSHCommand(command)
}

func (s *Service) isSafeReadOnlySSHProfileCall(profileID, command string) bool {
	if ok, _ := dataprofiles.IsReadOnlySSHCommand(command); !ok {
		return false
	}
	if s == nil {
		return false
	}
	s.mu.Lock()
	checker := s.dataProfileChecker
	s.mu.Unlock()
	if checker == nil {
		return false
	}
	kind, ro, exists := checker(strings.TrimSpace(profileID))
	return exists && ro && kind == dataprofiles.KindSSH
}

func isMutatingDataProfileCall(input map[string]interface{}) bool {
	act := strings.ToLower(strings.TrimSpace(firstString(input["action"])))
	switch act {
	case "list", "test":
		return false
	case "http_request":
		m := strings.ToUpper(strings.TrimSpace(firstString(input["method"])))
		return m != "" && m != "GET" && m != "HEAD"
	case "sql_query":
		ro, _ := dataprofiles.IsReadOnlySQL(firstString(input["query"]))
		return !ro
	case "ssh_exec":
		ro, _ := dataprofiles.IsReadOnlySSHCommand(firstString(input["command"]))
		return !ro
	default:
		b, _ := json.Marshal(input)
		_ = b
		return true
	}
}
