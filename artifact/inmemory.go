// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package artifact

import (
	"context"
	"fmt"
	"io/fs"
	"iter"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"google.golang.org/genai"
	"rsc.io/omap"
	"rsc.io/ordered"
)

// inMemoryService is an in-memory implementation of the Service.
// It is primarily for testing and demonstration purposes.
type inMemoryService struct {
	mu sync.RWMutex
	// ordered(appName, userID, sessionID) -> session
	artifacts omap.Map[string, *genai.Part]
}

// InMemoryService returns a new in-memory artifact service.
func InMemoryService() Service {
	return &inMemoryService{}
}

// fileHasUserNamespace checks if a filename indicates a user scoped artifact.
func fileHasUserNamespace(filename string) bool {
	return strings.HasPrefix(filename, "user:")
}

// userScopedArtifactKey defines the string for the session part used by user
// scope files. userScopedArtifactAppKey is the app part used for new user
// artifacts so project assets can be shared across agents/apps for the same
// user. Load/List still scan legacy app-scoped user artifacts for compatibility.
const userScopedArtifactKey = "user"
const userScopedArtifactAppKey = "__user__"

type artifactKey struct {
	AppName   string
	UserID    string
	SessionID string
	FileName  string
	Version   int64
}

// Encode encodes the artifactKey into a string.
func (ak artifactKey) Encode() string {
	return string(ordered.Encode(ak.AppName, ak.UserID, ak.SessionID, ak.FileName, ordered.Rev(ak.Version)))
}

// Decode decodes the string key into an artifactKey.
func (ak *artifactKey) Decode(key string) error {
	var v ordered.Reverse[int64]
	err := ordered.Decode([]byte(key), &ak.AppName, &ak.UserID, &ak.SessionID, &ak.FileName, &v)
	if err != nil {
		return err
	}
	ak.Version = v.Value()
	return nil
}

// scan returns an iterator over all key-value pairs
// in the range begin ≤ key ≤ end.
// TODO: add a concurrent tests.
func (s *inMemoryService) scan(lo, hi string) iter.Seq2[artifactKey, *genai.Part] {
	return func(yield func(key artifactKey, val *genai.Part) bool) {
		for k, val := range s.artifacts.Scan(lo, hi) {
			var key artifactKey
			if err := key.Decode(k); err != nil {
				continue
			}

			if !yield(key, val) {
				return
			}
		}
	}
}

func (s *inMemoryService) find(appName, userID, sessionID, fileName string) (int64, *genai.Part, bool) {
	lo := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName, Version: math.MaxInt64}.Encode()
	hi := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName, Version: 0}.Encode()
	for key, val := range s.scan(lo, hi) {
		// first key is the latest one.
		return key.Version, val, true
	}
	return 0, nil, false
}

func (s *inMemoryService) get(appName, userID, sessionID, fileName string, version int64) (*genai.Part, bool) {
	key := artifactKey{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		FileName:  fileName,
		Version:   version,
	}.Encode()
	return s.artifacts.Get(key)
}

func (s *inMemoryService) set(appName, userID, sessionID, fileName string, version int64, artifact *genai.Part) {
	key := artifactKey{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		FileName:  fileName,
		Version:   version,
	}.Encode()
	s.artifacts.Set(key, artifact)
}

func (s *inMemoryService) delete(appName, userID, sessionID, fileName string, version int64) {
	key := artifactKey{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		FileName:  fileName,
		Version:   version,
	}.Encode()
	s.artifacts.Delete(key)
}

// Save implements [artifact.Service]
func (s *inMemoryService) Save(ctx context.Context, req *SaveRequest) (*SaveResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	artifact := req.Part
	// If file is user scoped, store it under user scope path
	if fileHasUserNamespace(fileName) {
		appName = userScopedArtifactAppKey
		sessionID = userScopedArtifactKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	nextVersion := int64(1)
	if internalVer, _, ok := s.find(appName, userID, sessionID, fileName); ok {
		nextVersion = internalVer + 1
	}
	s.set(appName, userID, sessionID, fileName, nextVersion, artifact)
	return &SaveResponse{Version: nextVersion}, nil
}

// Delete implements [artifact.Service]
func (s *inMemoryService) Delete(ctx context.Context, req *DeleteRequest) error {
	err := req.Validate()
	if err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	version := req.Version
	// If file is user scoped, adjust artifactKey part
	if fileHasUserNamespace(fileName) {
		appName = userScopedArtifactAppKey
		sessionID = userScopedArtifactKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if version != 0 {
		s.delete(appName, userID, sessionID, fileName, version)
		return nil
	}

	// pick the latest version
	lo := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName, Version: math.MaxInt64}.Encode()
	hi := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName}.Encode()
	s.artifacts.DeleteRange(lo, hi)
	return nil
}

// Load implements [artifact.Service]
func (s *inMemoryService) Load(ctx context.Context, req *LoadRequest) (*LoadResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	version := req.Version
	// If file is user scoped, adjust artifactKey part
	if fileHasUserNamespace(fileName) {
		sessionID = userScopedArtifactKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if fileHasUserNamespace(fileName) {
		return s.loadUserScopedLocked(appName, userID, fileName, version)
	}

	if version > 0 {
		artifact, ok := s.get(appName, userID, sessionID, fileName, version)
		if !ok {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		return &LoadResponse{Part: artifact}, nil
	}
	// pick the latest version
	_, artifact, ok := s.find(appName, userID, sessionID, fileName)
	if !ok {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return &LoadResponse{Part: artifact}, nil
}

// List implements [artifact.Service]
func (s *inMemoryService) List(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID := req.AppName, req.UserID, req.SessionID
	s.mu.RLock()
	defer s.mu.RUnlock()

	files := map[string]bool{}
	lo := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID}.Encode()
	hi := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID + "\x00"}.Encode()
	// TODO(hyangah): extend omap to search key only and skip value decoding.
	for key := range s.scan(lo, hi) {
		if key.SessionID != sessionID { // scan includes key matching `hi`
			continue
		}
		files[key.FileName] = true
	}

	// Besides the session specific artifacts, also retrieve user scoped artifacts
	// from the shared app namespace and legacy app-scoped namespaces.
	for key, _ := range s.artifacts.All() {
		var decoded artifactKey
		if err := decoded.Decode(key); err != nil {
			continue
		}
		if decoded.UserID != userID || decoded.SessionID != userScopedArtifactKey || !fileHasUserNamespace(decoded.FileName) {
			continue
		}
		files[decoded.FileName] = true
	}

	filenames := slices.Collect(maps.Keys(files))
	sort.Strings(filenames)
	return &ListResponse{FileNames: filenames}, nil
}

// Versions implements [artifact.Service] and returns an error if no versions are found.
func (s *inMemoryService) Versions(ctx context.Context, req *VersionsRequest) (*VersionsResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	if fileHasUserNamespace(fileName) {
		appName = userScopedArtifactAppKey
		sessionID = userScopedArtifactKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var versions []int64
	lo := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName, Version: math.MaxInt64}.Encode()
	hi := artifactKey{AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName}.Encode()
	// TODO(hyangah): extend omap to search key only and skip value decoding.
	for key := range s.scan(lo, hi) {
		versions = append(versions, key.Version)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return &VersionsResponse{Versions: versions}, nil
}

// GetArtifactVersion implements [artifact.Service] and returns the metadata for a specific version.
func (s *inMemoryService) GetArtifactVersion(ctx context.Context, req *GetArtifactVersionRequest) (*GetArtifactVersionResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName, version := req.AppName, req.UserID, req.SessionID, req.FileName, req.Version
	if fileHasUserNamespace(fileName) {
		sessionID = userScopedArtifactKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var artifact *genai.Part
	var ok bool
	if fileHasUserNamespace(fileName) {
		if version > 0 {
			for _, candidateApp := range s.userScopedAppCandidatesLocked(appName, userID, fileName) {
				if artifact, ok = s.get(candidateApp, userID, sessionID, fileName, version); ok {
					break
				}
			}
		} else {
			version, artifact, ok = s.findLatestUserScopedLocked(appName, userID, fileName)
		}
	} else if version > 0 {
		artifact, ok = s.get(appName, userID, sessionID, fileName, version)
	} else {
		version, artifact, ok = s.find(appName, userID, sessionID, fileName)
	}

	if !ok {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}

	mimeType := "text/plain"
	if artifact != nil && artifact.InlineData != nil {
		mimeType = artifact.InlineData.MIMEType
	}

	return &GetArtifactVersionResponse{
		ArtifactVersion: &ArtifactVersion{
			Version:  version,
			MimeType: mimeType,
		},
	}, nil
}

func (s *inMemoryService) loadUserScopedLocked(appName, userID, fileName string, version int64) (*LoadResponse, error) {
	if version > 0 {
		for _, candidateApp := range s.userScopedAppCandidatesLocked(appName, userID, fileName) {
			if artifact, ok := s.get(candidateApp, userID, userScopedArtifactKey, fileName, version); ok {
				return &LoadResponse{Part: artifact}, nil
			}
		}
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	_, artifact, ok := s.findLatestUserScopedLocked(appName, userID, fileName)
	if !ok {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return &LoadResponse{Part: artifact}, nil
}

func (s *inMemoryService) findLatestUserScopedLocked(appName, userID, fileName string) (int64, *genai.Part, bool) {
	for _, candidateApp := range s.userScopedAppCandidatesLocked(appName, userID, fileName) {
		if version, artifact, ok := s.find(candidateApp, userID, userScopedArtifactKey, fileName); ok {
			return version, artifact, true
		}
	}
	return 0, nil, false
}

func (s *inMemoryService) userScopedAppCandidatesLocked(appName, userID, fileName string) []string {
	seen := map[string]bool{}
	add := func(v string, out *[]string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		*out = append(*out, v)
	}
	out := []string{}
	add(userScopedArtifactAppKey, &out)
	add(appName, &out)
	for key := range s.artifacts.All() {
		var decoded artifactKey
		if err := decoded.Decode(key); err != nil {
			continue
		}
		if decoded.UserID == userID && decoded.SessionID == userScopedArtifactKey && decoded.FileName == fileName {
			add(decoded.AppName, &out)
		}
	}
	return out
}

var _ Service = (*inMemoryService)(nil)
