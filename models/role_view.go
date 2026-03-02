package models

import "strings"

var AllowedRoleViewIDs = []string{
	"dashboard",
	"movements",
	"new",
	"gastos",
	"pedidos",
	"wallet",
	"payroll",
	"reports",
	"billing",
	"cashout",
	"cashout-bank",
	"admin/users",
	"admin/roles",
	"admin/categories",
}

var mandatoryViewsByRole = map[string][]string{
	"admin": {"admin/roles"},
}

var allowedRoleViewSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(AllowedRoleViewIDs))
	for _, id := range AllowedRoleViewIDs {
		result[id] = struct{}{}
	}
	return result
}()

func IsAllowedRoleView(id string) bool {
	_, ok := allowedRoleViewSet[strings.TrimSpace(id)]
	return ok
}

func CanonicalizeRoleViews(views []string) []string {
	selected := make(map[string]struct{}, len(views))
	for _, view := range views {
		safe := strings.TrimSpace(view)
		if safe == "" {
			continue
		}
		if !IsAllowedRoleView(safe) {
			continue
		}
		selected[safe] = struct{}{}
	}

	result := make([]string, 0, len(selected))
	for _, id := range AllowedRoleViewIDs {
		if _, ok := selected[id]; ok {
			result = append(result, id)
		}
	}

	return result
}

func EnsureMandatoryRoleViews(role string, views []string) []string {
	normalizedRole := strings.ToLower(strings.TrimSpace(role))
	result := CanonicalizeRoleViews(views)

	mandatory := mandatoryViewsByRole[normalizedRole]
	if len(mandatory) == 0 {
		return result
	}

	result = append(result, mandatory...)
	return CanonicalizeRoleViews(result)
}

func DefaultAdminRoleViews() []string {
	return append([]string{}, EnsureMandatoryRoleViews("admin", AllowedRoleViewIDs)...)
}
