package main

import (
	"net/http"

	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/teal-fm/piper/internal/telemetry"
	"github.com/teal-fm/piper/session"
)

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, handler http.Handler) {
		mux.Handle(pattern, telemetry.Route(pattern, handler))
	}
	handleFunc := func(pattern string, handler http.HandlerFunc) {
		handle(pattern, handler)
	}

	handleFunc("/failed-plays", session.WithAuth(failedPlays(app.database, app.pages, app.atprotoService, viper.GetString("server.root_url")), app.sessionManager))

	//Handles static file routes
	handle("/static/{file_name}", app.pages.Static())

	handleFunc("/", session.WithPossibleAuth(home(app.database, app.pages, app.lastfmService, app.atprotoService, app.buildTime), app.sessionManager))

	// OAuth Routes
	handleFunc("/login/atproto", app.oauthManager.HandleLogin("atproto"))
	handleFunc("/callback/atproto", session.WithPossibleAuth(app.oauthManager.HandleCallback("atproto"), app.sessionManager)) // Use possible auth
	handleFunc("/logout", app.oauthManager.HandleLogout("atproto"))
	handleFunc("/debug/", session.WithAuth(app.sessionManager.HandleDebug, app.sessionManager))

	// Authenticated Web Routes
	handleFunc("/current-track", session.WithAuth(app.spotifyService.HandleCurrentTrack, app.sessionManager))
	handleFunc("/history", session.WithAuth(app.spotifyService.HandleTrackHistory, app.sessionManager))
	handleFunc("/api-keys", session.WithAuth(app.apiKeyService.HandleAPIKeyManagement(app.database, app.pages, viper.GetString("server.root_url")), app.sessionManager))
	handleFunc("/unlink-spotify", session.WithAuth(handleUnlinkSpotify(app.database, app.spotifyService), app.sessionManager))
	handleFunc("/login/spotify", session.WithAuth(app.oauthManager.HandleLogin("spotify"), app.sessionManager))
	handleFunc("/callback/spotify", session.WithAuth(app.oauthManager.HandleCallback("spotify"), app.sessionManager))

	// Last.fm
	handleFunc("/link-lastfm", session.WithAuth(handleLinkLastfmForm(app.database, app.pages), app.sessionManager)) // GET form
	handleFunc("/link-lastfm/submit", session.WithAuth(handleLinkLastfmSubmit(app.database), app.sessionManager))   // POST submit - Changed route slightly
	handleFunc("/unlink-lastfm", session.WithAuth(handleUnlinkLastfm(app.database), app.sessionManager))

	// ListenBrainz
	handleFunc("/link-listenbrainz", session.WithAuth(handleLinkListenBrainz(app.database, app.pages, app.listenBrainzService), app.sessionManager))
	handleFunc("/unlink-listenbrainz", session.WithAuth(handleUnlinkListenBrainz(app.database, app.listenBrainzService), app.sessionManager))

	// Apple Music
	handleFunc("/link-applemusic", session.WithAuth(handleAppleMusicLink(app.database, app.pages, app.appleMusicService), app.sessionManager))
	handleFunc("/unlink-applemusic", session.WithAuth(handleUnlinkAppleMusic(app.database), app.sessionManager))

	// API routes
	handleFunc("/api/v1/me", session.WithAPIAuth(apiMeHandler(app.database), app.sessionManager))
	handleFunc("/api/v1/lastfm", session.WithAPIAuth(apiGetLastfmUserHandler(app.database), app.sessionManager))
	handleFunc("/api/v1/lastfm/set", session.WithAPIAuth(apiLinkLastfmHandler(app.database), app.sessionManager))
	handleFunc("/api/v1/lastfm/unset", session.WithAPIAuth(apiUnlinkLastfmHandler(app.database), app.sessionManager))
	handleFunc("/api/v1/listenbrainz", session.WithAPIAuth(apiGetListenBrainzHandler(app.database), app.sessionManager))
	handleFunc("/api/v1/listenbrainz/set", session.WithAPIAuth(apiLinkListenBrainzHandler(app.database, app.listenBrainzService), app.sessionManager))
	handleFunc("/api/v1/listenbrainz/unset", session.WithAPIAuth(apiUnlinkListenBrainzHandler(app.database, app.listenBrainzService), app.sessionManager))
	handleFunc("/api/v1/current-track", session.WithAPIAuth(apiCurrentTrack(app.spotifyService), app.sessionManager)) // Spotify Current
	handleFunc("/api/v1/history", session.WithAPIAuth(apiTrackHistory(app.spotifyService), app.sessionManager))       // Spotify History
	handleFunc("/api/v1/musicbrainz/search", apiMusicBrainzSearch(app.mbService))                                     // MusicBrainz (public?)

	// Apple Music user authorization (protected with session auth)
	handleFunc("/api/v1/applemusic/authorize", session.WithAuth(apiAppleMusicAuthorize(app.database), app.sessionManager))
	handleFunc("/api/v1/applemusic/unlink", session.WithAuth(apiAppleMusicUnlink(app.database), app.sessionManager))

	// ListenBrainz-compatible endpoint
	handleFunc("/1/submit-listens", session.WithAPIAuth(apiSubmitListensHandler(app.database, app.atprotoService, app.playingNowService, app.mbService), app.sessionManager))
	handleFunc("/1/validate-token", apiMbTokenValidateHandler(app.sessionManager))

	serverUrlRoot := viper.GetString("server.root_url")
	handleFunc("/oauth-client-metadata.json", func(w http.ResponseWriter, r *http.Request) {
		app.atprotoService.HandleClientMetadata(w, r, serverUrlRoot)
	})
	handleFunc("/oauth/jwks.json", app.atprotoService.HandleJwks)

	standard := alice.New()
	return standard.Then(mux)
}
