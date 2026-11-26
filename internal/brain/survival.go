package brain

import (
	"fmt"

	"github.com/hectorgimenez/koolo/internal/config"
)

// SurvivalSettings are the potion and chicken thresholds (percent of life)
// the AI may tune between games. Values are always clamped to safe bounds.
type SurvivalSettings struct {
	HealingPotionAt     int `json:"healingPotionAt"`
	RejuvPotionAtLife   int `json:"rejuvPotionAtLife"`
	ChickenAt           int `json:"chickenAt"`
	MercHealingPotionAt int `json:"mercHealingPotionAt"`
	MercChickenAt       int `json:"mercChickenAt"`
}

// Bounds stop the AI from choosing something suicidal (chicken 0) or silly
// (drinking potions at 99%).
var survivalBounds = map[string][2]int{
	"healingPotionAt":     {40, 90},
	"rejuvPotionAtLife":   {20, 60},
	"chickenAt":           {15, 50},
	"mercHealingPotionAt": {30, 85},
	"mercChickenAt":       {0, 40},
}

func clamp(name string, v int) int {
	b := survivalBounds[name]
	if v < b[0] {
		return b[0]
	}
	if v > b[1] {
		return b[1]
	}
	return v
}

func (s SurvivalSettings) clamped() SurvivalSettings {
	s.HealingPotionAt = clamp("healingPotionAt", s.HealingPotionAt)
	s.RejuvPotionAtLife = clamp("rejuvPotionAtLife", s.RejuvPotionAtLife)
	s.ChickenAt = clamp("chickenAt", s.ChickenAt)
	s.MercHealingPotionAt = clamp("mercHealingPotionAt", s.MercHealingPotionAt)
	s.MercChickenAt = clamp("mercChickenAt", s.MercChickenAt)
	// A chicken threshold at or above the rejuv threshold means rejuvs never get used.
	if s.ChickenAt >= s.RejuvPotionAtLife {
		s.ChickenAt = s.RejuvPotionAtLife - 5
		s.ChickenAt = clamp("chickenAt", s.ChickenAt)
	}
	return s
}

func currentSurvival(cfg *config.CharacterCfg) SurvivalSettings {
	h := cfg.Health
	return SurvivalSettings{
		HealingPotionAt:     h.HealingPotionAt,
		RejuvPotionAtLife:   h.RejuvPotionAtLife,
		ChickenAt:           h.ChickenAt,
		MercHealingPotionAt: h.MercHealingPotionAt,
		MercChickenAt:       h.MercChickenAt,
	}
}

func applySurvival(cfg *config.CharacterCfg, s SurvivalSettings) {
	cfg.Health.HealingPotionAt = s.HealingPotionAt
	cfg.Health.RejuvPotionAtLife = s.RejuvPotionAtLife
	cfg.Health.ChickenAt = s.ChickenAt
	cfg.Health.MercHealingPotionAt = s.MercHealingPotionAt
	cfg.Health.MercChickenAt = s.MercChickenAt
}

func (s SurvivalSettings) String() string {
	return fmt.Sprintf("drink healing potion at %d%% life, rejuvenation at %d%%, chicken (leave game) at %d%%, merc potion at %d%%, merc chicken at %d%%",
		s.HealingPotionAt, s.RejuvPotionAtLife, s.ChickenAt, s.MercHealingPotionAt, s.MercChickenAt)
}

func boundsText() string {
	return fmt.Sprintf("Allowed ranges: healingPotionAt %v, rejuvPotionAtLife %v, chickenAt %v, mercHealingPotionAt %v, mercChickenAt %v.",
		survivalBounds["healingPotionAt"], survivalBounds["rejuvPotionAtLife"], survivalBounds["chickenAt"],
		survivalBounds["mercHealingPotionAt"], survivalBounds["mercChickenAt"])
}
