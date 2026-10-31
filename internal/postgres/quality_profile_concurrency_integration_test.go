package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"uuid"
)

func storedQualityProfileVersion(t *testing.T, ctx context.Context, store *Store, id uuid.UUID) uint64 {
	t.Helper()
	return storedQualityProfile(t, ctx, store, id).PolicyVersion
}

func storedQualityProfile(t *testing.T, ctx context.Context, store *Store, id uuid.UUID) QualityProfile {
	t.Helper()
	profiles, err := store.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.ID == id {
			return profile
		}
	}
	t.Fatalf("quality profile %s not found", id)
	return QualityProfile{}
}

func storedRules(t *testing.T, profile QualityProfile) []releases.Rule {
	t.Helper()
	rules, err := releases.ParsePolicyRules(profile.Policy)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func requiredAudioPolicy(t *testing.T, audio string) json.RawMessage {
	t.Helper()
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{
		{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
		{Name: "Required audio codec", Fact: "audio_codec", AllowedValues: []string{audio}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func resolutionOnlyPolicy(t *testing.T) json.RawMessage {
	t.Helper()
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{
		{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// A stale editor must not be able to replace a newer policy, and above all must
// not remove the required rules the newer policy added.
func TestUpdateQualityProfileRejectsStaleEditAndKeepsNewerRequiredRules(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	read := storedQualityProfileVersion(t, ctx, store, fixture.profile.ID)
	profile := fixture.profile

	newer := requiredAudioPolicy(t, "eac3")
	saved, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: profile.Name, Policy: newer, UpgradesEnabled: true, Default: true, Enabled: true}, read)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PolicyVersion != read+1 {
		t.Fatalf("version after a current edit=%d, want %d", saved.PolicyVersion, read+1)
	}
	afterNewer := storedQualityProfile(t, ctx, store, profile.ID)

	// The second editor read the same version, so it is now stale. It also tries to
	// change every other field, none of which may land.
	_, err = store.UpdateQualityProfile(ctx, QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: "Stale rename", Policy: resolutionOnlyPolicy(t), UpgradesEnabled: false, Default: true, Enabled: false}, read)
	if !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("stale edit error=%v, want ErrConcurrentModification", err)
	}
	stored := storedQualityProfile(t, ctx, store, profile.ID)
	if !reflect.DeepEqual(stored, afterNewer) {
		t.Fatalf("a refused stale edit changed the profile:\n got  %+v\n want %+v", stored, afterNewer)
	}
	if !reflect.DeepEqual(storedRules(t, stored), storedRules(t, afterNewer)) || len(storedRules(t, stored)) != 2 {
		t.Fatalf("the newer required audio rule did not survive: %+v", storedRules(t, stored))
	}

	// Reloading and repeating the edit at the current version works and bumps once more.
	again, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: "Renamed after reload", Policy: newer, UpgradesEnabled: true, Default: true, Enabled: true}, stored.PolicyVersion)
	if err != nil {
		t.Fatal(err)
	}
	if again.PolicyVersion != read+2 || again.Name != "Renamed after reload" {
		t.Fatalf("retry result=%+v", again)
	}
}

func TestUpdateQualityProfileRequiresAnExpectedVersion(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	profile := fixture.profile
	before := storedQualityProfile(t, ctx, store, profile.ID)
	edit := QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: "Should not land", Policy: requiredAudioPolicy(t, "ac3"), UpgradesEnabled: true, Default: true, Enabled: true}

	if _, err := store.UpdateQualityProfile(ctx, edit, 0); !errors.Is(err, ErrExpectedPolicyVersionRequired) {
		t.Fatalf("zero expected version error=%v, want ErrExpectedPolicyVersionRequired", err)
	}
	// A version from the future is as wrong as one from the past.
	if _, err := store.UpdateQualityProfile(ctx, edit, before.PolicyVersion+5); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("future expected version error=%v, want ErrConcurrentModification", err)
	}
	// The embedded version in the body is not the expected version: only the argument is.
	edit.PolicyVersion = before.PolicyVersion
	if _, err := store.UpdateQualityProfile(ctx, edit, before.PolicyVersion+1); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("body version must not stand in for the expected version: %v", err)
	}
	// A missing profile is still not found, not a conflict.
	edit.ID = uuid.NewV7()
	if _, err := store.UpdateQualityProfile(ctx, edit, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown profile error=%v, want ErrNotFound", err)
	}
	if after := storedQualityProfile(t, ctx, store, profile.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused edits changed the profile: %+v", after)
	}
}

// The default flip on other profiles happens inside the update. A refused stale
// edit must leave the current default exactly where it was.
func TestStaleUpdateDoesNotMoveTheDefaultProfile(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	second, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Second movie profile", PolicyVersion: 1, Policy: resolutionOnlyPolicy(t), UpgradesEnabled: true, Default: false, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Another editor changes the second profile, so a form opened at version 1 is stale.
	if _, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: second.ID, MediaKind: second.MediaKind, Name: "Second movie profile (edited)", Policy: resolutionOnlyPolicy(t), UpgradesEnabled: true, Default: false, Enabled: true}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: second.ID, MediaKind: second.MediaKind, Name: "Second movie profile", Policy: resolutionOnlyPolicy(t), UpgradesEnabled: true, Default: true, Enabled: true}, 1); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("stale make-default error=%v, want ErrConcurrentModification", err)
	}
	if !storedQualityProfile(t, ctx, store, fixture.profile.ID).Default || storedQualityProfile(t, ctx, store, second.ID).Default {
		t.Fatal("a refused stale edit moved the default profile")
	}
}

// An editor opened before a preset was applied must not overwrite the preset
// result, including the required rules the preset left in place.
func TestEditOpenedBeforeAPresetApplicationIsRefused(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	profile := fixture.profile
	read := storedQualityProfileVersion(t, ctx, store, profile.ID)
	if _, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: profile.Name, Policy: requiredAudioPolicy(t, "eac3"), UpgradesEnabled: true, Default: true, Enabled: true}, read); err != nil {
		t.Fatal(err)
	}
	current := storedQualityProfile(t, ctx, store, profile.ID) // an editor opens here, at current.PolicyVersion

	rules, err := releases.ApplyPreset(storedRules(t, current), releases.PresetBalanced)
	if err != nil {
		t.Fatal(err)
	}
	withPreset, err := releases.WithPolicyRules(current.Policy, rules)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateQualityProfilePolicyAtVersion(ctx, profile.ID, withPreset, current.PolicyVersion); err != nil {
		t.Fatal(err)
	}
	applied := storedQualityProfile(t, ctx, store, profile.ID)

	_, err = store.UpdateQualityProfile(ctx, QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: profile.Name, Policy: current.Policy, UpgradesEnabled: true, Default: true, Enabled: true}, current.PolicyVersion)
	if !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("edit opened before the preset error=%v, want ErrConcurrentModification", err)
	}
	if after := storedQualityProfile(t, ctx, store, profile.ID); !reflect.DeepEqual(after, applied) {
		t.Fatalf("the stale edit replaced the preset result: %+v", after)
	}
}

// The row lock serializes real concurrent editors: of N that read the same
// version, exactly one wins and the rest are refused rather than layered on top.
func TestConcurrentProfileEditorsOnlyOneWins(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	profile := fixture.profile
	read := storedQualityProfileVersion(t, ctx, store, profile.ID)

	const editors = 8
	audio := []string{"aac", "ac3", "dts", "eac3", "flac", "mp3", "opus", "truehd"}
	errs := make([]error, editors)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < editors; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errs[index] = store.UpdateQualityProfile(ctx, QualityProfile{ID: profile.ID, MediaKind: profile.MediaKind, Name: profile.Name, Policy: requiredAudioPolicy(t, audio[index]), UpgradesEnabled: true, Default: true, Enabled: true}, read)
		}(index)
	}
	close(start)
	wait.Wait()

	winner := -1
	for index, err := range errs {
		switch {
		case err == nil:
			if winner >= 0 {
				t.Fatalf("editors %d and %d both won at version %d", winner, index, read)
			}
			winner = index
		case !errors.Is(err, ErrConcurrentModification):
			t.Fatalf("editor %d error=%v, want nil or ErrConcurrentModification", index, err)
		}
	}
	if winner < 0 {
		t.Fatal("no editor won")
	}
	stored := storedQualityProfile(t, ctx, store, profile.ID)
	if stored.PolicyVersion != read+1 {
		t.Fatalf("version=%d, want exactly one bump to %d", stored.PolicyVersion, read+1)
	}
	var want []releases.Rule
	if err := json.Unmarshal(requiredAudioPolicy(t, audio[winner]), &struct {
		Rules *[]releases.Rule `json:"rules"`
	}{Rules: &want}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedRules(t, stored), want) {
		t.Fatalf("stored rules are not the winner's: %+v", storedRules(t, stored))
	}
}
