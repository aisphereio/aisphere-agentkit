// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package skillservice

import (
	"context"
	"io"
	"sort"

	"google.golang.org/adk/tool/skilltoolset/skill"
)

type filteredSource struct {
	base    skill.Source
	allowed map[string]bool
	ordered []string
}

func NewFilteredSource(base skill.Source, selected []string) skill.Source {
	allowed := make(map[string]bool, len(selected))
	ordered := append([]string(nil), selected...)
	sort.Strings(ordered)
	for _, name := range ordered {
		allowed[name] = true
	}
	return &filteredSource{base: base, allowed: allowed, ordered: ordered}
}

func (s *filteredSource) ListFrontmatters(ctx context.Context) ([]*skill.Frontmatter, error) {
	out := []*skill.Frontmatter{}
	for _, name := range s.ordered {
		fm, err := s.base.LoadFrontmatter(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, fm)
	}
	return out, nil
}

func (s *filteredSource) ListResources(ctx context.Context, name, subpath string) ([]string, error) {
	if !s.allowed[name] {
		return nil, skill.ErrSkillNotFound
	}
	return s.base.ListResources(ctx, name, subpath)
}

func (s *filteredSource) LoadFrontmatter(ctx context.Context, name string) (*skill.Frontmatter, error) {
	if !s.allowed[name] {
		return nil, skill.ErrSkillNotFound
	}
	return s.base.LoadFrontmatter(ctx, name)
}

func (s *filteredSource) LoadInstructions(ctx context.Context, name string) (string, error) {
	if !s.allowed[name] {
		return "", skill.ErrSkillNotFound
	}
	return s.base.LoadInstructions(ctx, name)
}

func (s *filteredSource) LoadResource(ctx context.Context, name, resourcePath string) (io.ReadCloser, error) {
	if !s.allowed[name] {
		return nil, skill.ErrSkillNotFound
	}
	return s.base.LoadResource(ctx, name, resourcePath)
}
