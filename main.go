package main

import (
	"context"
	"encoding/json"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
	"html/template"
	"log"
	"net/http"
	"os"
)

type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURI  string `json:"redirect_uri"`
}

type PlaylistBackup struct {
	Name   string        `json:"name"`
	Tracks []TrackBackup `json:"tracks"`
}

type TrackBackup struct {
	Name   string `json:"name"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	URI    string `json:"uri"`
}

var (
	auth       *spotifyauth.Authenticator
	state      = "abc123"
	backupData []PlaylistBackup
	userToken  *oauth2.Token
)

func removeDuplicateTracks(tracks []TrackBackup) []TrackBackup {
	seen := make(map[string]bool)
	result := []TrackBackup{}

	for _, track := range tracks {
		if !seen[track.URI] {
			seen[track.URI] = true
			result = append(result, track)
		}
	}
	return result
}

func getPlaylistTracks(client *spotify.Client, ctx context.Context, playlistID spotify.ID) ([]spotify.PlaylistItem, error) {
	var tracks []spotify.PlaylistItem
	limit := 100
	offset := 0

	for {
		tracksPage, err := client.GetPlaylistItems(ctx, playlistID, spotify.Limit(limit), spotify.Offset(offset))
		if err != nil {
			return nil, err
		}

		tracks = append(tracks, tracksPage.Items...)

		if len(tracksPage.Items) < limit {
			break
		}
		offset += limit
	}

	return tracks, nil
}

func main() {
	config := &Config{}
	file, _ := os.ReadFile("config.json")
	json.Unmarshal(file, config)

	auth = spotifyauth.New(
		spotifyauth.WithRedirectURL(config.RedirectURI),
		spotifyauth.WithScopes(
			spotifyauth.ScopePlaylistReadPrivate,
			spotifyauth.ScopePlaylistReadCollaborative,
			spotifyauth.ScopeUserLibraryRead,
			spotifyauth.ScopePlaylistModifyPublic,
			spotifyauth.ScopePlaylistModifyPrivate,
		),
		spotifyauth.WithClientID(config.ClientID),
		spotifyauth.WithClientSecret(config.ClientSecret),
	)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl := template.Must(template.ParseFiles("templates/index.html"))
		tmpl.Execute(w, nil)
	})

	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		url := auth.AuthURL(state)
		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	})

	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		tok, err := auth.Token(ctx, state, r)
		if err != nil {
			log.Printf("Error getting token: %v", err)
			http.Error(w, "Couldn't get token", http.StatusForbidden)
			return
		}

		userToken = tok

		client := spotify.New(auth.Client(ctx, tok))
		playlists, err := client.CurrentUsersPlaylists(ctx)
		if err != nil {
			log.Printf("Error getting playlists: %v", err)
			http.Error(w, "Couldn't get playlists", http.StatusInternalServerError)
			return
		}

		backupData = make([]PlaylistBackup, 0)

		for _, playlist := range playlists.Playlists {
			tracks, err := getPlaylistTracks(client, ctx, playlist.ID)
			if err != nil {
				continue
			}

			playlistBackup := PlaylistBackup{
				Name:   playlist.Name,
				Tracks: make([]TrackBackup, 0),
			}

			for _, track := range tracks {
				if track.Track.Track.Name == "" {
					continue
				}

				trackBackup := TrackBackup{
					Name:   track.Track.Track.Name,
					Artist: track.Track.Track.Artists[0].Name,
					Album:  track.Track.Track.Album.Name,
					URI:    string(track.Track.Track.URI),
				}
				playlistBackup.Tracks = append(playlistBackup.Tracks, trackBackup)
			}

			backupData = append(backupData, playlistBackup)
		}

		tmpl := template.Must(template.ParseFiles("templates/playlists.html"))
		tmpl.Execute(w, backupData)
	})

	http.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=spotify_backup.json")
		json.NewEncoder(w).Encode(backupData)
	})

	http.HandleFunc("/restore", func(w http.ResponseWriter, r *http.Request) {
		if userToken == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if r.Method == "GET" {
			tmpl := template.Must(template.ParseFiles("templates/restore.html"))
			tmpl.Execute(w, nil)
			return
		}

		file, _, err := r.FormFile("backup")
		if err != nil {
			http.Error(w, "Error reading file", http.StatusBadRequest)
			return
		}
		defer file.Close()

		var playlists []PlaylistBackup
		if err := json.NewDecoder(file).Decode(&playlists); err != nil {
			http.Error(w, "Error parsing JSON", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		client := spotify.New(auth.Client(ctx, userToken))
		user, err := client.CurrentUser(ctx)
		if err != nil {
			http.Error(w, "Error getting user info", http.StatusInternalServerError)
			return
		}

		const batchSize = 100
		for _, playlist := range playlists {
			log.Printf("Processing playlist: %s", playlist.Name)

			playlist.Tracks = removeDuplicateTracks(playlist.Tracks)

			newPlaylist, err := client.CreatePlaylistForUser(ctx, user.ID, playlist.Name, "", false, false)
			if err != nil {
				continue
			}

			var trackIDs []spotify.ID
			for _, track := range playlist.Tracks {
				trackID := spotify.ID(track.URI[14:])
				trackIDs = append(trackIDs, trackID)
			}

			totalTracks := len(trackIDs)
			processedTracks := 0

			for i := 0; i < len(trackIDs); i += batchSize {
				end := i + batchSize
				if end > len(trackIDs) {
					end = len(trackIDs)
				}

				currentBatch := trackIDs[i:end]
				processedTracks += len(currentBatch)
				progress := float64(processedTracks) / float64(totalTracks) * 100

				log.Printf("Playlist: %s - Progress: %.2f%% - Adding tracks %d-%d of %d",
					playlist.Name,
					progress,
					i+1,
					end,
					totalTracks,
				)

				_, err = client.AddTracksToPlaylist(ctx, newPlaylist.ID, currentBatch...)
				if err != nil {
					log.Printf("Error adding tracks batch to playlist %s: %v", playlist.Name, err)
					continue
				}
			}

			log.Printf("Completed playlist: %s - Added %d unique tracks", playlist.Name, totalTracks)
		}

		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	log.Printf("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
