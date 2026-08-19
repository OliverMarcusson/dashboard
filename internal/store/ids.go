package store

import "github.com/google/uuid"

func newID() string { return uuid.NewString() }

// NewID exposes identifier generation to other packages so the format stays
// consistent across tables.
func NewID() string { return newID() }
