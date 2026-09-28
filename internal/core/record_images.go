package core

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
)

const maxRecordImageBytes = 20 << 20

// RecordImage is stored outside State so a state refresh never transfers the
// Base64 screenshot bytes. Notes refer to it through /api/record-images/<id>.
type RecordImage struct {
	ID, MimeType string
	Data         []byte
}

var inlineRecordImageRE = regexp.MustCompile(`!\[([^\]]*)\]\(data:(image/[A-Za-z0-9.+-]+);base64,([A-Za-z0-9+/=\r\n]+)\)`)

func (s *Service) externalizeRecordImages(ctx context.Context, note string) (string, error) {
	var saveErr error
	out := inlineRecordImageRE.ReplaceAllStringFunc(note, func(match string) string {
		if saveErr != nil {
			return match
		}
		parts := inlineRecordImageRE.FindStringSubmatch(match)
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(parts[3], "\n", ""), "\r", ""))
		// Preserve malformed legacy content rather than making /api/state fail.
		// Valid record images are externalized; invalid data remains readable as-is.
		if err != nil || len(data) == 0 || len(data) > maxRecordImageBytes {
			return match
		}
		image := RecordImage{ID: ID(), MimeType: parts[2], Data: data}
		if err = s.repo.SaveRecordImage(ctx, image); err != nil {
			saveErr = err
			return match
		}
		return "![" + parts[1] + "](/api/record-images/" + image.ID + ")"
	})
	return out, saveErr
}

// migrateRecordImages upgrades existing persisted Base64 notes exactly once.
func (s *Service) migrateRecordImages(ctx context.Context, state *State) (bool, error) {
	changed := false
	for i := range state.Records {
		note, err := s.externalizeRecordImages(ctx, state.Records[i].Note)
		if err != nil {
			return false, err
		}
		if note != state.Records[i].Note {
			state.Records[i].Note = note
			changed = true
		}
	}
	return changed, nil
}

func (s *Service) RecordImage(ctx context.Context, id string) (RecordImage, error) {
	return s.repo.GetRecordImage(ctx, id)
}
