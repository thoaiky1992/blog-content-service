package domain

import "github.com/google/uuid"

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleEditor   Role = "editor"
	RoleReader   Role = "reader"
	RoleReporter Role = "reporter"
)

type Permission string

const (
	PermBlogRead     Permission = "blog:read"
	PermBlogCreate   Permission = "blog:create"
	PermBlogUpdate   Permission = "blog:update"
	PermBlogDelete   Permission = "blog:delete"
	PermReportExport Permission = "report:export"
)

// rolePermissions is the role permission matrix.
var rolePermissions = map[Role]map[Permission]bool{
	RoleAdmin: {
		PermBlogRead: true, PermBlogCreate: true, PermBlogUpdate: true,
		PermBlogDelete: true, PermReportExport: true,
	},
	RoleEditor:   {PermBlogRead: true, PermBlogCreate: true, PermBlogUpdate: true},
	RoleReader:   {PermBlogRead: true},
	RoleReporter: {PermBlogRead: true, PermReportExport: true},
}

// anonymousPermissions applies to requests without an authenticated user.
var anonymousPermissions = map[Permission]bool{PermBlogRead: true}

// Actor is the caller. The zero value is the anonymous actor.
type Actor struct {
	ID   uuid.UUID
	Role Role
}

func Anonymous() Actor { return Actor{} }

func (a Actor) IsAnonymous() bool { return a.ID == uuid.Nil }

// Can reports whether the actor holds the permission.
func (a Actor) Can(p Permission) bool {
	if a.IsAnonymous() {
		return anonymousPermissions[p]
	}
	return rolePermissions[a.Role][p]
}

// Require returns nil if the actor holds p. Otherwise it returns
// ErrUnauthenticated for an anonymous actor or ErrForbidden for an authenticated one.
func (a Actor) Require(p Permission) error {
	if a.Can(p) {
		return nil
	}
	if a.IsAnonymous() {
		return ErrUnauthenticated
	}
	return ErrForbidden
}
