package access

import (
	"errors"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"tailscale.com/tailcfg"
)

func ident(user tailcfg.UserID, tags []string, roles map[authz.Role][]string, public ...string) authz.Identity {
	return authz.Identity{UserID: user, Tags: tags, Grants: authz.Effective{Patterns: roles, PublicPatterns: public}}
}

var (
	owner       = ident(1, nil, map[authz.Role][]string{authz.RoleReceiver: {"dev"}, authz.RoleSender: {"dev"}})
	other       = ident(2, nil, map[authz.Role][]string{authz.RoleReceiver: {"*"}})
	adminAll    = ident(3, nil, map[authz.Role][]string{authz.RoleAdmin: {"*"}})
	adminProd   = ident(4, nil, map[authz.Role][]string{authz.RoleAdmin: {"prod"}})
	taggedAdmin = ident(0, []string{"tag:ci"}, map[authz.Role][]string{authz.RoleAdmin: {"*"}})
	noRole      = ident(5, nil, nil)
	ownerNoRole = ident(1, nil, nil)
	ownerProd   = ident(1, nil, map[authz.Role][]string{authz.RoleReceiver: {"prod"}})
	ownerPublic = ident(1, nil, map[authz.Role][]string{authz.RoleReceiver: {"dev"}}, "dev")

	devHook = Hook{Owner: 1, Channel: "dev"}
)

func TestRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  any
		want any
	}{
		{"View owner", View(owner, devHook), Owner},
		{"View other", View(other, devHook), None},
		{"View adminAll", View(adminAll, devHook), Admin},
		{"View adminProd", View(adminProd, devHook), None},
		{"View taggedAdmin", View(taggedAdmin, devHook), None},
		{"View ownerNoRole", View(ownerNoRole, devHook), Owner},
		{"IsAdmin adminAll", IsAdmin(adminAll), true},
		{"IsAdmin taggedAdmin", IsAdmin(taggedAdmin), false},
		{"IsAdmin owner", IsAdmin(owner), false},
		{"MayCreate owner", MayCreate(owner), true},
		{"MayCreate noRole", MayCreate(noRole), false},
		{"MayAttach owner", MayAttach(owner), true},
		{"MayAttach ownerNoRole", MayAttach(ownerNoRole), false},
		{"MayAttach taggedReceiver", MayAttach(ident(0, []string{"tag:ci"}, map[authz.Role][]string{authz.RoleReceiver: {"*"}})), false},
		{"MayReceive owner dev", MayReceive(owner.Grants, "dev"), true},
		{"MayReceive owner prod", MayReceive(owner.Grants, "prod"), false},
		{"MayPublish ownerPublic dev", MayPublish(ownerPublic.Grants, "dev"), true},
		{"MayPublish owner dev", MayPublish(owner.Grants, "dev"), false},
		{"Edit owner", Edit(owner, devHook), nil},
		{"Edit other", Edit(other, devHook), ErrHidden},
		{"Edit adminAll", Edit(adminAll, devHook), ErrForbidden},
		{"Edit ownerNoRole", Edit(ownerNoRole, devHook), ErrForbidden},
		{"Edit ownerProd", Edit(ownerProd, devHook), nil},
		{"Act owner", Act(owner, devHook), nil},
		{"Act adminAll", Act(adminAll, devHook), ErrForbidden},
		{"Act other", Act(other, devHook), ErrHidden},
		{"Act ownerNoRole", Act(ownerNoRole, devHook), ErrForbidden},
		{"Act ownerProd", Act(ownerProd, devHook), ErrForbidden},
		{"Delete owner", Delete(owner, devHook), nil},
		{"Delete ownerNoRole", Delete(ownerNoRole, devHook), nil},
		{"Delete adminAll", Delete(adminAll, devHook), nil},
		{"Delete adminProd", Delete(adminProd, devHook), ErrHidden},
		{"Serve owner", Serve(owner, devHook), nil},
		{"Serve other", Serve(other, devHook), ErrOtherUser},
		{"Serve taggedAdmin", Serve(taggedAdmin, devHook), ErrNotUser},
		{"Serve ownerNoRole", Serve(ownerNoRole, devHook), ErrNoReceiver},
		{"Serve ownerProd", Serve(ownerProd, devHook), ErrChannel},
		{"ServeInternet owner", ServeInternet(owner, devHook), ErrNoPublic},
		{"ServeInternet ownerPublic", ServeInternet(ownerPublic, devHook), nil},
		{"ServeInternet other", ServeInternet(other, devHook), ErrOtherUser},
		{"MaySend owner", MaySend(owner), true},
		{"MaySend other", MaySend(other), false},
		{"Send owner", Send(owner, devHook), true},
		{"Send other", Send(other, devHook), false},
		{"Send owner prod", Send(owner, Hook{Owner: 1, Channel: "prod"}), false},
		{"MayStream noRole", MayStream(noRole), false},
		{"MayStream taggedAdmin", MayStream(taggedAdmin), false},
		{"MayStream taggedSender", MayStream(ident(0, []string{"tag:ci"}, map[authz.Role][]string{authz.RoleSender: {"*"}})), true},
		{"MayStream owner", MayStream(owner), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e, ok := tc.want.(error); ok {
				if !errors.Is(tc.got.(error), e) {
					t.Fatalf("got %v, want %v", tc.got, e)
				}
				return
			}
			if tc.want == nil {
				if tc.got != nil {
					t.Fatalf("got %v, want nil", tc.got)
				}
				return
			}
			if tc.got != tc.want {
				t.Fatalf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}
