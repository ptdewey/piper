package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	spotifyOauth "golang.org/x/oauth2/spotify"

	"github.com/teal-fm/piper/config"
	"github.com/teal-fm/piper/db"
	"github.com/teal-fm/piper/internal/telemetry"
	"github.com/teal-fm/piper/models"
	"github.com/teal-fm/piper/oauth"
	"github.com/teal-fm/piper/oauth/atproto"
	"github.com/teal-fm/piper/pages"
	apikeyService "github.com/teal-fm/piper/service/apikey"
	"github.com/teal-fm/piper/service/applemusic"
	"github.com/teal-fm/piper/service/lastfm"
	"github.com/teal-fm/piper/service/listenbrainz"
	"github.com/teal-fm/piper/service/musicbrainz"
	"github.com/teal-fm/piper/service/playingnow"
	"github.com/teal-fm/piper/service/spotify"
	"github.com/teal-fm/piper/session"
)

type application struct {
	database            *db.DB
	sessionManager      *session.Manager
	oauthManager        *oauth.ServiceManager
	spotifyService      *spotify.Service
	lastfmService       *lastfm.Service
	listenBrainzService *listenbrainz.Service
	apiKeyService       *apikeyService.Service
	mbService           *musicbrainz.Service
	atprotoService      *atproto.AuthService
	playingNowService   *playingnow.Service
	appleMusicService   *applemusic.Service
	pages               *pages.Pages
	buildTime           time.Time
}

// JSON API handlers

func jsonResponse(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if data != nil {
		err := json.NewEncoder(w).Encode(data)
		if err != nil {
			log.Printf("Error encoding JSON response: %v", err)
			return
		}
	}
}

func main() {
	config.Load()
	skipDatabaseClose := false
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	telemetrySDK, err := telemetry.NewSDK(ctx, models.SubmissionAgent)
	if err != nil {
		log.Printf("OpenTelemetry initialization failed; continuing without export: %v", err)
	}
	defer func() {
		if telemetrySDK != nil {
			if err := telemetrySDK.Shutdown(context.Background()); err != nil {
				log.Printf("OpenTelemetry shutdown failed: %v", err)
			}
		}
	}()

	database, err := db.New(viper.GetString("db.path"))
	if err != nil {
		log.Printf("Error connecting to database: %v", err)
		return
	}
	defer func() {
		if !skipDatabaseClose {
			_ = database.Close()
		}
	}()

	if err := database.Initialize(); err != nil {
		log.Printf("Error initializing database: %v", err)
		return
	}

	sessionManager := session.NewSessionManager(database)

	// --- Service Initializations ---

	var newJwkPrivateKey = viper.GetString("atproto.client_secret_key")
	if newJwkPrivateKey == "" {
		fmt.Printf("You now have to set the ATPROTO_CLIENT_SECRET_KEY env var to a private key. This can be done via goat key generate -t P-256")
		return
	}
	var clientSecretKeyId = viper.GetString("atproto.client_secret_key_id")
	if clientSecretKeyId == "" {
		fmt.Printf("You also now have to set the ATPROTO_CLIENT_SECRET_KEY_ID env var to a key ID. This needs to be persistent and unique. Here's one for you: %d", time.Now().Unix())
		return
	}

	var allowedDids = viper.GetStringSlice("allowed_dids")
	if len(allowedDids) > 0 {
		log.Printf("Allowed DIDs provided. Only allowing %s\n", strings.Join(allowedDids, ", "))
	}

	atprotoService, err := atproto.NewATprotoAuthService(
		database,
		sessionManager,
		newJwkPrivateKey,
		viper.GetString("atproto.client_id"),
		viper.GetString("atproto.callback_url"),
		clientSecretKeyId,
		allowedDids,
	)
	if err != nil {
		log.Printf("Error creating ATproto auth service: %v", err)
		return
	}

	mbService := musicbrainz.NewMusicBrainzService(database)
	playingNowService := playingnow.NewPlayingNowService(database, atprotoService, mbService)

	// Check feature toggles for music services
	enableSpotify := viper.GetBool("enable_spotify")
	enableLastFM := viper.GetBool("enable_lastfm")
	enableListenBrainz := viper.GetBool("enable_listenbrainz")
	enableAppleMusic := viper.GetBool("enable_applemusic")

	var spotifyService *spotify.Service
	var lastfmService *lastfm.Service
	var listenBrainzService *listenbrainz.Service
	var appleMusicService *applemusic.Service

	// Initialize Spotify service if enabled and credentials are present
	if enableSpotify {
		clientID := viper.GetString("spotify.client_id")
		clientSecret := viper.GetString("spotify.client_secret")

		if clientID != "" && clientSecret != "" {
			spotifyService = spotify.NewSpotifyService(database, atprotoService, mbService, playingNowService)
			log.Println("Spotify service enabled and configured")
		} else {
			log.Println("Spotify enabled but credentials missing (client_id or client_secret). Spotify features will be disabled.")
			viper.Set("enable_spotify", false)
		}
	} else {
		log.Println("Spotify service disabled via ENABLE_SPOTIFY=false")
	}

	// Initialize Last.fm service if enabled and API key is present
	if enableLastFM {
		apiKey := viper.GetString("lastfm.api_key")

		if apiKey != "" {
			lastfmService = lastfm.NewLastFMService(database, apiKey, mbService, atprotoService, playingNowService)
			log.Println("Last.fm service enabled and configured")
		} else {
			log.Println("Last.fm enabled but API key missing. Last.fm features will be disabled.")
			viper.Set("enable_lastfm", false)
		}
	} else {
		log.Println("Last.fm service disabled via ENABLE_LASTFM=false")
	}

	if enableListenBrainz {
		if err := listenbrainz.ValidateAPIURL(viper.GetString("listenbrainz.api_url")); err != nil {
			log.Printf("Invalid ListenBrainz API URL: %v", err)
			return
		}
		contactURL := viper.GetString("server.root_url")
		if contactURL == "" {
			contactURL = "https://teal.fm"
		}
		listenBrainzService = listenbrainz.NewService(
			database,
			viper.GetString("listenbrainz.api_url"),
			fmt.Sprintf("%s (%s)", models.SubmissionAgent, contactURL),
			mbService,
			atprotoService,
			playingNowService,
		)
		log.Println("ListenBrainz service enabled")
	} else {
		log.Println("ListenBrainz service disabled via ENABLE_LISTENBRAINZ=false")
	}

	// Initialize Apple Music service if enabled and credentials are present
	if enableAppleMusic {
		// Read Apple Music settings with env fallbacks
		teamID := viper.GetString("applemusic.team_id")
		if teamID == "" {
			teamID = viper.GetString("APPLE_MUSIC_TEAM_ID")
		}
		keyID := viper.GetString("applemusic.key_id")
		if keyID == "" {
			keyID = viper.GetString("APPLE_MUSIC_KEY_ID")
		}
		keyPath := viper.GetString("applemusic.private_key_path")
		if keyPath == "" {
			keyPath = viper.GetString("APPLE_MUSIC_PRIVATE_KEY_PATH")
		}

		// Only initialize if all required credentials are present
		if teamID != "" && keyID != "" && keyPath != "" {
			appleMusicService = applemusic.NewService(
				teamID,
				keyID,
				keyPath,
			).WithPersistence(
				func() (string, time.Time, bool, error) {
					return database.GetAppleMusicDeveloperToken()
				},
				func(token string, exp time.Time) error {
					return database.SaveAppleMusicDeveloperToken(token, exp)
				},
			).WithDeps(database, atprotoService, mbService, playingNowService)
			log.Println("Apple Music service enabled and configured")
		} else {
			log.Println("Apple Music enabled but credentials missing (team_id, key_id, or private_key_path). Apple Music features will be disabled.")
			viper.Set("enable_applemusic", false)
		}
	} else {
		log.Println("Apple Music service disabled via ENABLE_APPLEMUSIC=false")
	}

	oauthManager := oauth.NewOAuthServiceManager()

	// Register Spotify OAuth service only if Spotify is enabled and configured
	if spotifyService != nil {
		spotifyOAuth := oauth.NewOAuth2Service(
			oauth2.Config{
				ClientID:     viper.GetString("spotify.client_id"),
				ClientSecret: viper.GetString("spotify.client_secret"),
				RedirectURL:  viper.GetString("callback.spotify"),
				Scopes:       viper.GetStringSlice("spotify.scopes"),
				Endpoint:     spotifyOauth.Endpoint,
			},
			spotifyService,
			log.Default(),
			func(ctx context.Context) int64 {
				id, _ := session.GetUserID(ctx)
				return id
			},
		)
		oauthManager.RegisterService("spotify", spotifyOAuth)
		log.Println("Spotify OAuth service registered")
	}

	oauthManager.RegisterService("atproto", atprotoService)

	apiKeyService := apikeyService.NewAPIKeyService(database, sessionManager)

	app := &application{
		database:            database,
		sessionManager:      sessionManager,
		oauthManager:        oauthManager,
		apiKeyService:       apiKeyService,
		mbService:           mbService,
		spotifyService:      spotifyService,
		lastfmService:       lastfmService,
		listenBrainzService: listenBrainzService,
		atprotoService:      atprotoService,
		playingNowService:   playingNowService,
		appleMusicService:   appleMusicService,
		pages:               pages.NewPages(),
		buildTime:           resolveBuildTime(),
	}

	trackerInterval := time.Duration(viper.GetInt("tracker.interval")) * time.Second

	var pollers sync.WaitGroup
	startPoller := func(run func()) {
		pollers.Add(1)
		go func() {
			defer pollers.Done()
			run()
		}()
	}

	// Start Spotify listening tracker if service is configured
	if spotifyService != nil {
		startPoller(func() { spotifyService.StartListeningTracker(ctx, trackerInterval) })
		log.Println("Spotify listening tracker started")
	}

	// Start Last.fm listening tracker if service is configured
	if lastfmService != nil {
		lastfmInterval := time.Duration(viper.GetInt("lastfm.interval_seconds")) * time.Second
		if lastfmInterval <= 0 {
			lastfmInterval = 30 * time.Second
		}
		startPoller(func() { lastfmService.StartListeningTracker(ctx, lastfmInterval) })
		log.Println("Last.fm listening tracker started")
	}

	if listenBrainzService != nil {
		listenBrainzInterval := time.Duration(viper.GetInt("listenbrainz.interval_seconds")) * time.Second
		startPoller(func() { listenBrainzService.StartListeningTracker(ctx, listenBrainzInterval) })
		log.Println("ListenBrainz listening tracker started")
	}

	// Start Apple Music tracker if service is configured
	if appleMusicService != nil {
		startPoller(func() { appleMusicService.StartListeningTracker(ctx, trackerInterval) })
		log.Println("Apple Music listening tracker started")
	}

	serverAddr := fmt.Sprintf("%s:%s", viper.GetString("server.host"), viper.GetString("server.port"))
	server := &http.Server{
		Addr:         serverAddr,
		Handler:      app.routes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	fmt.Printf("Server running at: http://%s\n", serverAddr)
	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			log.Printf("HTTP server failed: %v", err)
		}
		cancel()
	}

	httpShutdownCtx, httpShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := server.Shutdown(httpShutdownCtx); err != nil {
		log.Printf("HTTP server shutdown failed: %v", err)
	}
	httpShutdownCancel()

	pollersDone := make(chan struct{})
	go func() {
		pollers.Wait()
		close(pollersDone)
	}()
	pollerShutdownCtx, pollerShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	select {
	case <-pollersDone:
	case <-pollerShutdownCtx.Done():
		log.Printf("Timed out waiting for listening trackers to stop")
		pollerShutdownCancel()
		skipDatabaseClose = true
		return
	}
	pollerShutdownCancel()

}
