package extras

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Stats records every finished game and a sample of level/experience every interval, detects
// milestones and death streaks, and computes the numbers for the dashboard (and for /improve-koolo).

type GameRecord struct {
	Time       time.Time `json:"time"`
	Character  string    `json:"character"`
	Result     string    `json:"result"` // ok | death | chicken | merc chicken | error
	Version    string    `json:"version"`
	Level      int       `json:"level"`
	Experience int       `json:"experience"`
}

type Sample struct {
	Time       time.Time `json:"time"`
	Character  string    `json:"character"`
	Level      int       `json:"level"`
	Experience int       `json:"experience"`
	Gold       int       `json:"gold"`
	InGame     bool      `json:"inGame"`
}

type Stats struct {
	prev        map[string]CharStatus
	prevLevel   map[string]int
	deathStreak map[string]int
}

func (s *Stats) Name() string { return "stats" }

func (s *Stats) Run(ctx context.Context, env *Env) error {
	s.prev, s.prevLevel, s.deathStreak = map[string]CharStatus{}, map[string]int{}, map[string]int{}
	every := time.Duration(max(10, env.Cfg.Stats.IntervalSeconds)) * time.Second
	for {
		s.tick(ctx, env)
		if !sleep(ctx, every) {
			return nil
		}
	}
}

func (s *Stats) tick(ctx context.Context, env *Env) {
	st, err := GetStatus(ctx, env.Koolo)
	if err != nil {
		if !errors.Is(err, ErrKooloDown) {
			env.Log.Warn("stats: status", "error", err)
		}
		return
	}
	metrics, err := GetMetrics(ctx, env.Koolo)
	if err != nil {
		metrics = map[string]CharMetrics{} // older Koolo without koolo_metrics: games still count
	}
	now := env.Now()
	for name, cs := range st.Characters {
		m := metrics[name]
		prev, seen := s.prev[name]
		s.prev[name] = cs
		if m.Level > 0 {
			_ = env.Store.Append("samples.jsonl", Sample{Time: now, Character: name, Level: m.Level,
				Experience: m.Experience, Gold: m.Gold, InGame: m.InGame})
			s.checkMilestone(env, name, m.Level)
		}
		if !seen || cs.Games <= prev.Games {
			continue // first look, or Koolo restarted (counters reset)
		}
		for i := prev.Games; i < cs.Games; i++ {
			result := cs.LastEnd
			if i < cs.Games-1 {
				result = inferResult(prev, cs) // several games finished between two looks
			}
			_ = env.Store.Append("games.jsonl", GameRecord{Time: now, Character: name, Result: result,
				Version: st.Version, Level: m.Level, Experience: m.Experience})
			env.Bus.Publish(Event{Kind: EvNewGame, Text: name + ": " + result, Data: map[string]string{"character": name, "result": result}})
			s.checkStreak(env, name, result)
		}
	}
}

// inferResult guesses the result of a game we only saw in the counters.
func inferResult(prev, cur CharStatus) string {
	switch {
	case cur.Deaths > prev.Deaths:
		return "death"
	case cur.Chickens > prev.Chickens:
		return "chicken"
	case cur.Errors > prev.Errors:
		return "error"
	}
	return "ok"
}

func (s *Stats) checkStreak(env *Env, name, result string) {
	if result == "death" {
		s.deathStreak[name]++
	} else {
		s.deathStreak[name] = 0
	}
	if n := env.Cfg.Stats.DeathStreak; n > 0 && s.deathStreak[name] == n {
		env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("%s died %d games in a row. Time for a brain check or easier runs.", name, n)})
	}
}

func (s *Stats) checkMilestone(env *Env, name string, level int) {
	before := s.prevLevel[name]
	s.prevLevel[name] = level
	if before == 0 || level <= before {
		return
	}
	for _, ms := range env.Cfg.Stats.MilestoneLevels {
		if before < ms && level >= ms {
			text := fmt.Sprintf("%s reached level %d!", name, ms)
			env.Bus.Publish(Event{Kind: EvMilestone, Text: text, Data: map[string]string{"character": name, "level": fmt.Sprint(ms)}})
			env.Bus.Publish(Event{Kind: EvAlert, Text: "🎉 " + text})
		}
	}
}

// ---------------------------------------------------------------------------
// Summary for the dashboard
// ---------------------------------------------------------------------------

type CharSummary struct {
	Character    string        `json:"character"`
	GamesToday   int           `json:"gamesToday"`
	GamesTotal   int           `json:"gamesTotal"`
	DeathsToday  int           `json:"deathsToday"`
	DeathsTotal  int           `json:"deathsTotal"`
	Chickens     int           `json:"chickens"`
	Errors       int           `json:"errors"`
	DeathRate    float64       `json:"deathRate"` // deaths per game, all time
	Level        int           `json:"level"`
	ExpPerHour   int           `json:"expPerHour"` // over the last hour of samples
	LevelHistory []LevelPoint  `json:"levelHistory"`
	ByVersion    []VersionStat `json:"byVersion"`
}

type LevelPoint struct {
	Time  time.Time `json:"t"`
	Level int       `json:"level"`
}

type VersionStat struct {
	Version   string  `json:"version"`
	Games     int     `json:"games"`
	DeathRate float64 `json:"deathRate"`
}

func Summarize(store *Store, now time.Time) []CharSummary {
	games, _ := ReadAll[GameRecord](store, "games.jsonl")
	samples, _ := ReadAll[Sample](store, "samples.jsonl")
	by := map[string]*CharSummary{}
	get := func(n string) *CharSummary {
		if by[n] == nil {
			by[n] = &CharSummary{Character: n}
		}
		return by[n]
	}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	versions := map[string]map[string][2]int{} // char -> version -> [games, deaths]
	for _, g := range games {
		c := get(g.Character)
		c.GamesTotal++
		death := g.Result == "death"
		if death {
			c.DeathsTotal++
		}
		if g.Result == "chicken" || g.Result == "merc chicken" {
			c.Chickens++
		}
		if g.Result == "error" {
			c.Errors++
		}
		if !g.Time.Before(today) {
			c.GamesToday++
			if death {
				c.DeathsToday++
			}
		}
		if versions[g.Character] == nil {
			versions[g.Character] = map[string][2]int{}
		}
		v := versions[g.Character][g.Version]
		v[0]++
		if death {
			v[1]++
		}
		versions[g.Character][g.Version] = v
	}
	lastLevel := map[string]int{}
	var hourFirst, hourLast = map[string]Sample{}, map[string]Sample{}
	for _, sm := range samples {
		c := get(sm.Character)
		c.Level = sm.Level
		if sm.Level != lastLevel[sm.Character] {
			c.LevelHistory = append(c.LevelHistory, LevelPoint{Time: sm.Time, Level: sm.Level})
			lastLevel[sm.Character] = sm.Level
		}
		if now.Sub(sm.Time) <= time.Hour {
			if _, ok := hourFirst[sm.Character]; !ok {
				hourFirst[sm.Character] = sm
			}
			hourLast[sm.Character] = sm
		}
	}
	for name, first := range hourFirst {
		last := hourLast[name]
		if hrs := last.Time.Sub(first.Time).Hours(); hrs >= 0.1 && last.Experience >= first.Experience {
			get(name).ExpPerHour = int(float64(last.Experience-first.Experience) / hrs)
		}
	}
	out := make([]CharSummary, 0, len(by))
	for name, c := range by {
		if c.GamesTotal > 0 {
			c.DeathRate = float64(c.DeathsTotal) / float64(c.GamesTotal)
		}
		for v, n := range versions[name] {
			vs := VersionStat{Version: v, Games: n[0]}
			if n[0] > 0 {
				vs.DeathRate = float64(n[1]) / float64(n[0])
			}
			c.ByVersion = append(c.ByVersion, vs)
		}
		sort.Slice(c.ByVersion, func(i, j int) bool { return c.ByVersion[i].Version < c.ByVersion[j].Version })
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Character < out[j].Character })
	return out
}
