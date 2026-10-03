package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Event is one graded submission, appended to the log as a JSON line.
// Input, Expected and Got are only recorded for failures.
type Event struct {
	Time      time.Time `json:"time"`
	Key       string    `json:"key"`
	Challenge string    `json:"challenge"` // slug
	Variant   string    `json:"variant"`
	Pass      bool      `json:"pass"`
	Diff      string    `json:"diff,omitempty"`
	Input     string    `json:"input,omitempty"`
	Expected  string    `json:"expected,omitempty"`
	Got       string    `json:"got,omitempty"`
}

type issued struct {
	token   string
	variant string
	inst    *Inst
}

type ChalState struct {
	Passed   map[string]time.Time // variant -> first pass
	Attempts int
	Fails    int
	FirstTry int // 0 none yet, 1 pass, -1 fail
	SolvedAt time.Time
	Failures []*Event
	cur      *issued
}

type KeyState struct {
	Key         string
	First, Last time.Time
	Chals       map[string]*ChalState // by slug
}

type Store struct {
	mu   sync.Mutex
	keys map[string]*KeyState
	log  *os.File
}

func OpenStore(path string) (*Store, error) {
	s := &Store{keys: map[string]*KeyState{}}
	if path == "" {
		return s, nil
	}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<26)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				s.apply(&e)
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.log = f
	return s, nil
}

func (s *Store) key(k string, t time.Time) *KeyState {
	ks := s.keys[k]
	if ks == nil {
		ks = &KeyState{Key: k, First: t, Chals: map[string]*ChalState{}}
		s.keys[k] = ks
	}
	ks.Last = t
	return ks
}

func (ks *KeyState) chal(slug string) *ChalState {
	cs := ks.Chals[slug]
	if cs == nil {
		cs = &ChalState{Passed: map[string]time.Time{}}
		ks.Chals[slug] = cs
	}
	return cs
}

// apply folds a submission event into the state. Caller holds the lock
// (or is replaying at startup).
func (s *Store) apply(e *Event) {
	cs := s.key(e.Key, e.Time).chal(e.Challenge)
	cs.Attempts++
	if cs.FirstTry == 0 {
		cs.FirstTry = map[bool]int{true: 1, false: -1}[e.Pass]
	}
	if !e.Pass {
		cs.Fails++
		cs.Failures = append(cs.Failures, e)
		return
	}
	if _, ok := cs.Passed[e.Variant]; !ok {
		cs.Passed[e.Variant] = e.Time
	}
	if c := challengeBySlug(e.Challenge); c != nil && cs.SolvedAt.IsZero() && cs.solved(c) {
		cs.SolvedAt = e.Time
	}
}

func (cs *ChalState) solved(c *Challenge) bool {
	if cs == nil {
		return false
	}
	for _, v := range c.Vars {
		if _, ok := cs.Passed[v]; !ok {
			return false
		}
	}
	return true
}

// record applies and persists an event. Caller holds the lock.
func (s *Store) record(e *Event) error {
	s.apply(e)
	if s.log == nil {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.log.Write(append(b, '\n'))
	return err
}
