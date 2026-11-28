package bot

import (
	"context"
	"errors"
	"fmt"
	"github.com/hectorgimenez/koolo/internal/brain"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/hectorgimenez/d2go/pkg/data/difficulty"
	"github.com/hectorgimenez/d2go/pkg/data/skill"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
	"github.com/hectorgimenez/koolo/internal/config"
	ct "github.com/hectorgimenez/koolo/internal/context"
	"github.com/hectorgimenez/koolo/internal/event"
	"github.com/hectorgimenez/koolo/internal/game"
	"github.com/hectorgimenez/koolo/internal/health"
	"github.com/hectorgimenez/koolo/internal/run"
	"github.com/hectorgimenez/koolo/internal/utils"
)

type SinglePlayerSupervisor struct {
	*baseSupervisor
}

func (s *SinglePlayerSupervisor) GetData() *game.Data {
	return s.bot.ctx.Data
}

func (s *SinglePlayerSupervisor) GetContext() *ct.Context {
	return s.bot.ctx
}

func NewSinglePlayerSupervisor(name string, bot *Bot, statsHandler *StatsHandler) (*SinglePlayerSupervisor, error) {
	bs, err := newBaseSupervisor(bot, name, statsHandler)
	if err != nil {
		return nil, err
	}

	return &SinglePlayerSupervisor{
		baseSupervisor: bs,
	}, nil
}

// Start will return error if it can not be started, otherwise will always return nil
func (s *SinglePlayerSupervisor) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel

	err := s.ensureProcessIsRunningAndPrepare()
	if err != nil {
		return fmt.Errorf("error preparing game: %w", err)
	}

	firstRun := true
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if firstRun {
				err = s.waitUntilCharacterSelectionScreen()
				if err != nil {
					return fmt.Errorf("error waiting for character selection screen: %w", err)
				}
			}

			// By this point, we should be in the character selection screen.
			if !s.bot.ctx.Manager.InGame() {
				// Create the game
				if err = s.HandleOutOfGameFlow(); err != nil {
					// Ignore loading screen errors or unhandled errors (for now) and try again
					if err.Error() == "loading screen" || err.Error() == "" {
						utils.Sleep(100)
						continue
					}

					s.bot.ctx.Logger.Error(fmt.Sprintf("Error creating new game: %s", err.Error()))
					continue
				}
			}

			runs := run.BuildRuns(s.bot.ctx.CharacterCfg)
			gameStart := time.Now()
			if config.Characters[s.name].Game.RandomizeRuns {
				rand.Shuffle(len(runs), func(i, j int) { runs[i], runs[j] = runs[j], runs[i] })
			}
			event.Send(event.GameCreated(event.Text(s.name, "New game created"), "", ""))
			s.bot.ctx.LastBuffAt = time.Time{}
			s.logGameStart(runs)

			// Refresh game data to make sure we have the latest information
			s.bot.ctx.RefreshGameData()

			// Let the AI brain choose which of the enabled runs to do, and in what order.
			if brain.Default.Enabled() {
				snap := brain.TakeSnapshot(s.bot.ctx.Data.Data, s.name, s.bot.ctx.CharacterCfg.Character.Class)
				planned := brain.Default.PlanGame(ctx, snap, s.bot.ctx.CharacterCfg)
				plannedCfg := *s.bot.ctx.CharacterCfg
				plannedCfg.Game.Runs = planned
				runs = run.BuildRuns(&plannedCfg)
			}

			// Perform keybindings check on the first run only
			if firstRun {
				// kooloforai patch: a level-1 leveling character set to Nightmare/Hell gets the wrong maps and buttons.
				if lvl, ok := s.bot.ctx.Data.PlayerUnit.FindStat(stat.Level, 0); ok {
					if msg := difficultyMismatch(s.bot.ctx.CharacterCfg, lvl.Value); msg != "" {
						s.bot.ctx.Logger.Warn(msg)
					}
				}
				missingKeybindings := s.bot.ctx.Char.CheckKeyBindings()
				// A bound Scroll of Town Portal does the job until a tome is bought (kooloforai patch).
				if _, ok := s.bot.ctx.Data.KeyBindings.TownPortalKB(); ok {
					kept := missingKeybindings[:0]
					for _, sk := range missingKeybindings {
						if sk != skill.TomeOfTownPortal {
							kept = append(kept, sk)
						}
					}
					missingKeybindings = kept
				}
				// Leveling characters bind their skills themselves in town (EnsureSkillBindings), so don't pause.
				if _, leveling := s.bot.ctx.Char.(ct.LevelingCharacter); leveling && len(missingKeybindings) > 0 {
					s.bot.ctx.Logger.Info("Missing key bindings will be bound automatically in town", slog.Any("skills", missingKeybindings))
					missingKeybindings = nil
				}
				if len(missingKeybindings) > 0 {
					var missingKeybindingsText = "Missing key binding for skill(s):"
					for _, v := range missingKeybindings {
						missingKeybindingsText += fmt.Sprintf("\n%s", skill.SkillNames[v])
					}
					missingKeybindingsText += "\nPlease bind the skills. Pausing bot..."

					s.bot.ctx.Logger.Warn("Bot paused: missing key binding(s). Bind them in D2R, then press Resume",
						slog.String("missing", strings.ReplaceAll(strings.TrimPrefix(missingKeybindingsText, "Missing key binding for skill(s):\n"), "\n", ", ")))
					utils.ShowDialog("Missing keybindings for "+s.bot.ctx.Name, missingKeybindingsText)
					s.TogglePause()
				}
			}

			err = s.bot.Run(ctx, firstRun, runs)
			firstRun = false

			var gameFinishReason event.FinishReason
			if err != nil {
				switch {
				case errors.Is(err, health.ErrChicken):
					gameFinishReason = event.FinishedChicken
				case errors.Is(err, health.ErrMercChicken):
					gameFinishReason = event.FinishedMercChicken
				case errors.Is(err, health.ErrDied):
					gameFinishReason = event.FinishedDied
				default:
					gameFinishReason = event.FinishedError
				}
				event.Send(event.GameFinished(event.WithScreenshot(s.name, err.Error(), s.bot.ctx.GameReader.Screenshot()), gameFinishReason))
				s.bot.ctx.Logger.Warn(
					fmt.Sprintf("Game finished with errors, reason: %s. Game total time: %0.2fs", err.Error(), time.Since(gameStart).Seconds()),
					slog.String("supervisor", s.name),
					slog.Uint64("mapSeed", uint64(s.bot.ctx.GameReader.MapSeed())),
				)
			} else {
				gameFinishReason = event.FinishedOK
				event.Send(event.GameFinished(event.Text(s.name, "Game finished successfully"), gameFinishReason))
			}

			if exitErr := s.bot.ctx.Manager.ExitGame(); exitErr != nil {
				errMsg := fmt.Sprintf("Error exiting game %s", exitErr.Error())
				event.Send(event.GameFinished(event.WithScreenshot(s.name, errMsg, s.bot.ctx.GameReader.Screenshot()), event.FinishedError))
				return errors.New(errMsg)
			}
		}
	}
}

// This function is responsible for handling all interactions with joining/creating games
func (s *SinglePlayerSupervisor) HandleOutOfGameFlow() error {
	// Refresh the data
	s.bot.ctx.RefreshGameData()

	// Catch-all
	if s.bot.ctx.CharacterCfg.AuthMethod != "None" && s.bot.ctx.GameReader.IsInCharacterSelectionScreen() && !s.bot.ctx.GameReader.IsOnline() {

		// Try and click the online tab to re-connect to bnet
		s.bot.ctx.HID.Click(game.LeftButton, 1090, 32) // click the online button

		// Wait a bit
		utils.Sleep(4000)

		// Check if we're online again, if not, kill the client
		if !s.bot.ctx.GameReader.IsOnline() {

			// Kill the client so the crash detector will restart it
			if err := s.KillClient(); err != nil {
				return err
			}

			return fmt.Errorf("we've lost connection to bnet or client glitched. The d2r process will be killed")
		}
	}

	// We're either in the in the Lobby or Character selection screen. Let's check
	if s.bot.ctx.GameReader.IsInCharacterSelectionScreen() {
		// TODO: Add Joining Games

		if s.bot.ctx.CharacterCfg.Game.CreateLobbyGames {
			retryCount := 0
			for !s.bot.ctx.GameReader.IsInLobby() {

				// Prevent an infinite loop
				if retryCount >= 5 && !s.bot.ctx.GameReader.IsInLobby() {
					return fmt.Errorf("failed to enter bnet lobby after 5 retries")
				}

				// Try to enter bnet lobby
				s.bot.ctx.HID.Click(game.LeftButton, 744, 650)
				utils.Sleep(1000)
			}

			if _, err := s.bot.ctx.Manager.CreateOnlineGame(s.bot.ctx.CharacterCfg.Game.PublicGameCounter); err != nil {
				s.bot.ctx.CharacterCfg.Game.PublicGameCounter++
				return fmt.Errorf("failed to create an online game")

			} else {
				// We created the game successfully!
				s.bot.ctx.CharacterCfg.Game.PublicGameCounter++
				return nil
			}
		} else {
			// TODO: Add logic to check if we're on the online or offline tab and handle it accordingly.
			if !s.bot.ctx.GameReader.IsOnline() && s.bot.ctx.CharacterCfg.AuthMethod != "None" {

				// Try and click the online tab to re-connect to bnet
				s.bot.ctx.HID.Click(game.LeftButton, 1090, 32) // click the online button

				// Wait a bit
				utils.Sleep(4000)

				// Check again
				if !s.bot.ctx.GameReader.IsOnline() {
					// We failed to re-connect. Kill the client so it will get re-started automatically.
					if err := s.KillClient(); err != nil {
						return err
					}
					return fmt.Errorf("lost connection to bnet, killing client")
				}
			}

			// Create the game
			if err := s.bot.ctx.Manager.NewGame(); err != nil {
				return fmt.Errorf("failed to create game: %w", err)
			}

			return nil
		}
	} else if s.bot.ctx.GameReader.IsInLobby() {
		// TODO: Add Joining Games

		// Check if we are suppose to create lobby games and enter lobby.
		if s.bot.ctx.CharacterCfg.Game.CreateLobbyGames {

			retryCount := 0
			for !s.bot.ctx.GameReader.IsInLobby() {

				// Prevent an infinite loop
				if retryCount >= 5 && !s.bot.ctx.GameReader.IsInLobby() {
					return fmt.Errorf("failed to enter bnet lobby after 5 retries")
				}

				// Try to enter bnet lobby
				s.bot.ctx.HID.Click(game.LeftButton, 744, 650)
				utils.Sleep(1000)
			}

			if _, err := s.bot.ctx.Manager.CreateOnlineGame(s.bot.ctx.CharacterCfg.Game.PublicGameCounter); err != nil {
				s.bot.ctx.CharacterCfg.Game.PublicGameCounter++
				return fmt.Errorf("failed to create an online game")

			} else {
				// We created the game successfully!
				s.bot.ctx.CharacterCfg.Game.PublicGameCounter++
				return nil
			}
		} else {
			// Press escape to exit the lobby
			s.bot.ctx.HID.PressKey(0x1B) // ESC - to avoid importing win here as well
			utils.Sleep(1000)

			for range 5 {
				if s.bot.ctx.GameReader.IsInCharacterSelectionScreen() && s.bot.ctx.GameReader.IsOnline() {
					break
				}

				if s.bot.ctx.GameReader.IsInLobby() {
					// Mission failed
					s.bot.ctx.HID.PressKey(0x1B) // ESC - to avoid importing win here as well
					utils.Sleep(1000)
				}
			}

			if !s.bot.ctx.GameReader.IsInCharacterSelectionScreen() {
				return fmt.Errorf("failed to leave lobby or an unknown case occurred")
			}

			// Create the game
			if err := s.bot.ctx.Manager.NewGame(); err != nil {
				return fmt.Errorf("failed to create game: %w", err)
			}
		}
	} else if s.bot.ctx.Data.OpenMenus.LoadingScreen {
		// We're in a loading screen, wait a bit
		utils.Sleep(250)
		return fmt.Errorf("loading screen")
	} else {
		return fmt.Errorf("")
	}

	// TODO: Maybe expand this with functionality to create new characters if the currently configured char isn't found? :)

	return nil
}

// difficultyMismatch warns when a leveling character's difficulty can't be unlocked yet at its level
// (Nightmare needs Baal in Normal, Hell needs Baal in Nightmare). Koolo uses this setting for the
// create-game button and for the maps, so a wrong one breaks pathing.
func difficultyMismatch(cfg *config.CharacterCfg, level int) string {
	leveling := false
	for _, r := range cfg.Game.Runs {
		if strings.HasPrefix(string(r), "leveling") {
			leveling = true
		}
	}
	if !leveling {
		return ""
	}
	switch {
	case cfg.Game.Difficulty == difficulty.Hell && level < 40, cfg.Game.Difficulty == difficulty.Nightmare && level < 20:
		return fmt.Sprintf("Difficulty is set to %s but the character is level %d: set Difficulty to normal in the character settings (leveling switches difficulty itself after Baal)", cfg.Game.Difficulty, level)
	}
	return ""
}
