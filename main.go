package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	_ "strconv"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	scs "github.com/alexedwards/scs/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/imdario/mergo"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
)

var (
	BuildTime  = "00000000.000000"
	CommitHash = "0000000"
	GoVersion  = "0.0"
	GitTag     = "0.0"
)

var layouts *template.Template
var sessionManager *scs.SessionManager
var dbpool *pgxpool.Pool
var layoutFuncs = template.FuncMap{
	"inc": func(i int) int {
		return i + 1
	},
	"dec": func(i int) int {
		return i - 1
	},
	"sum": func(a int, b int) int {
		return a + b
	},
	"sub": func(a int, b int) int {
		return a - b
	},
	"div": func(a int, b int) float64 {
		return float64(a) / float64(b)
	},
	"mult": func(a int, b int) int {
		return a * b
	},
}

func getWzProfile(id int, table string) map[string]interface{} {
	var name string
	var hash string
	var played int
	var wins int
	var losses int
	var elo int
	var pl map[string]interface{}
	req := "SELECT name, hash, autoplayed, autowon, autolost, elo FROM " + table + " WHERE id = $1 AND hidden = false AND deleted = false"
	derr := dbpool.QueryRow(context.Background(), req, id).
		Scan(&name, &hash, &played, &wins, &losses, &elo)
	if derr != nil {
		if derr != pgx.ErrNoRows {
			log.Println("getWzProfile: " + derr.Error())
		}
		return pl
	}
	pl = map[string]interface{}{
		"Id":     id,
		"Name":   name,
		"Hash":   hash,
		"Played": played,
		"Wins":   wins,
		"Losses": losses,
		"Elo":    elo,
	}
	return pl
}

func sessionAppendUser(r *http.Request, a *map[string]interface{}) *map[string]interface{} {
	if !sessionManager.Exists(r.Context(), "User.Username") || sessionManager.Get(r.Context(), "UserAuthorized") != "True" {
		return nil
	}
	var sessid int
	var sessuname string
	var sessfname string
	var sesslname string
	var sessemail string
	var sesseconf string
	var sessdisctoken string
	var sessdiscrefreshtoken string
	var sessdiscrefreshwhenepoch int
	var sessdiscstate string
	var sessdiscurl string
	var sesswzprofile int
	var sesswzprofile2 int
	var sessdisc map[string]interface{}
	// var sessvktoken string
	// var sessvkrefresh string
	// var sessvkrefreshwhenepoch int
	// var sessvkstate string
	// var sessvkurl string
	//
	//
	// coalesce(extract(epoch from vk_refresh_date), 0)::int
	// coalesce(vk_token, ''),
	// coalesce(vk_refresh, ''),
	// var sessvk map[string]interface{}

	if sessionManager.Exists(r.Context(), "User.Username") {
		sessuname = sessionManager.GetString(r.Context(), "User.Username")
		derr := dbpool.QueryRow(context.Background(), `
			SELECT id, email, fname, lname,
			coalesce(extract(epoch from email_confirmed), 0)::text,
			coalesce(discord_token, ''),
			coalesce(discord_refresh, ''),
			coalesce(extract(epoch from discord_refresh_date), 0)::int,
			coalesce(wzprofile, -1), coalesce(wzprofile2, -1)
			FROM users WHERE username = $1`, sessuname).
			Scan(&sessid, &sessemail, &sessfname, &sesslname, &sesseconf,
				&sessdisctoken, &sessdiscrefreshtoken, &sessdiscrefreshwhenepoch,
				&sesswzprofile, &sesswzprofile2)
			// &sessvktoken, &sessvkrefresh, &sessvkrefreshwhenepoch)
		if derr != nil {
			log.Println("sessionAppendUser: " + derr.Error())
		}
		sessdiscrefreshwhen := time.Unix(int64(sessdiscrefreshwhenepoch), 0)
		if sessdisctoken == "" {
			sessdiscstate = generateRandomString(32)
			sessdiscurl = DiscordGetUrl(sessdiscstate)
			sessionManager.Put(r.Context(), "User.Discord.State", sessdiscstate)
		} else {
			token := oauth2.Token{AccessToken: sessdisctoken, RefreshToken: sessdiscrefreshtoken, Expiry: sessdiscrefreshwhen}
			tokenold := token
			sessdisc = DiscordGetUInfo(&token)
			if token.AccessToken != tokenold.AccessToken || token.RefreshToken != tokenold.RefreshToken || token.Expiry != tokenold.Expiry {
				log.Println("Discord token refreshed")
				tag, derr := dbpool.Exec(context.Background(), "UPDATE users SET discord_token = $1, discord_refresh = $2, discord_refresh_date = $3 WHERE username = $4", token.AccessToken, token.RefreshToken, token.Expiry, sessionManager.Get(r.Context(), "User.Username"))
				if derr != nil {
					log.Println("Database call error: " + derr.Error())
				}
				if tag.RowsAffected() != 1 {
					log.Println("Database update error, rows affected " + string(tag))
				}
			}
			if token.AccessToken == "" {
				sessdiscstate = generateRandomString(32)
				sessdiscurl = DiscordGetUrl(sessdiscstate)
				sessionManager.Put(r.Context(), "User.Discord.State", sessdiscstate)
			}
			sessdisctoken = token.AccessToken
		}
		// sessvkrefreshwhen := time.Unix(int64(sessvkrefreshwhenepoch), 0)
		// if sessvktoken == "" {
		// 	sessvkstate = generateRandomString(32)
		// 	sessvkurl = VKGetUrl(sessvkstate)
		// 	sessionManager.Put(r.Context(), "User.VK.State", sessvkstate)
		// } else {
		// 	token := oauth2.Token{AccessToken: sessvktoken, RefreshToken: sessvkrefresh, Expiry: sessvkrefreshwhen}
		// 	log.Print("Refresh: " + sessvkrefresh)
		// 	tokenold := token
		// 	sessvk = VKGetUInfo(&token)
		// 	if token.AccessToken != tokenold.AccessToken || token.RefreshToken != tokenold.RefreshToken || token.Expiry != tokenold.Expiry {
		// 		log.Println("VK token refreshed")
		// 		tag, derr := dbpool.Exec(context.Background(), "UPDATE users SET vk_token = $1, vk_refresh = $2, vk_refresh_date = $3 WHERE username = $4", token.AccessToken, token.RefreshToken, token.Expiry, sessionManager.Get(r.Context(), "User.Username"))
		// 		if derr != nil {
		// 			log.Println("Database call error: " + derr.Error())
		// 		}
		// 		if tag.RowsAffected() != 1 {
		// 			log.Println("Database update error, rows affected " + string(tag))
		// 		}
		// 	}
		// 	if token.AccessToken == "" {
		// 		sessvkstate = generateRandomString(32)
		// 		sessvkurl = VKGetUrl(sessvkstate)
		// 		sessionManager.Put(r.Context(), "User.VK.State", sessvkstate)
		// 	}
		// 	sessvktoken = token.AccessToken
		// }
	}
	var usermap map[string]interface{}
	usermap = map[string]interface{}{
		"Username":   sessuname,
		"Id":         sessid,
		"Fname":      sessfname,
		"Lname":      sesslname,
		"Email":      sessemail,
		"Econf":      sesseconf,
		"WzProfile":  getWzProfile(sesswzprofile, "old_players3"),
		"WzProfile2": getWzProfile(sesswzprofile2, "players"),
		"Discord": map[string]interface{}{
			"Token":   sessdisctoken,
			"AuthUrl": sessdiscurl,
			"Data":    sessdisc,
		},
	}
	mergo.Merge(a, map[string]interface{}{
		"UserAuthorized": "True",
		"User":           usermap,
	})
	return a
}
func basicLayoutLookupRespond(page string, w http.ResponseWriter, r *http.Request, p interface{}) {
	in := layouts.Lookup(page)
	if in != nil {
		m, mk := p.(map[string]interface{})
		if mk == false {
			log.Println("Basic respond got parameters interface of wrong type")
		}
		m["NavWhere"] = page
		sessionAppendUser(r, &m)
		err := in.Execute(w, m)
		if err != nil {
			log.Println(err)
		}
	} else {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	}
}
func indexHandler(w http.ResponseWriter, r *http.Request) {
	basicLayoutLookupRespond("index", w, r, map[string]interface{}{})
}
func aboutHandler(w http.ResponseWriter, r *http.Request) {
	basicLayoutLookupRespond("about", w, r, map[string]interface{}{})
}
func legalHandler(w http.ResponseWriter, r *http.Request) {
	basicLayoutLookupRespond("legal", w, r, map[string]interface{}{})
}
func autohosterControllHandler(w http.ResponseWriter, r *http.Request) {
	basicLayoutLookupRespond("autohoster-controll", w, r, map[string]interface{}{})
}
func robotsHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "User-agent: *\nDisallow: /\n\n")
}
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./static/favicon.ico")
}
func ratingHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./static/favicon.ico")
}

type statusRespWr struct {
	http.ResponseWriter
	status int
}

func (w *statusRespWr) writeHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func customLogger(writer io.Writer, params handlers.LogFormatterParams) {
	r := params.Request
	ip := r.Header.Get("CF-Connecting-IP")
	geo := r.Header.Get("CF-IPCountry")
	ua := r.Header.Get("user-agent")
	log.Println("["+geo+" "+ip+"]", r.Method, params.StatusCode, r.RequestURI, "["+ua+"]")
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println()
	log.Println("TacticalPepe web server is starting up...")
	log.Printf("Built %s, Ver %s (%s)\n", BuildTime, GitTag, CommitHash)
	log.Println()
	rand.Seed(time.Now().UTC().UnixNano())
	log.Println("Loading enviroment")
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	DiscordVerifyEnv()
	VKVerifyEnv()

	log.Println("Loading layouts")
	layouts, err = template.New("main").Funcs(layoutFuncs).ParseGlob("layouts/*.gohtml")
	if err != nil {
		panic(err)
	}
	log.Println("Creating watcher")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()
	log.Println("Staring watcher")
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Println("event:", event)
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("Updating templates")
					nlayouts, err := template.New("main").Funcs(layoutFuncs).ParseGlob("layouts/*.gohtml")
					if err != nil {
						log.Println("Error while parsing templates:", err.Error())
					} else {
						layouts = nlayouts.Funcs(layoutFuncs)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()
	err = watcher.Add("layouts/")
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Connecting to database")
	dbpool, err = pgxpool.Connect(context.Background(), os.Getenv("DB"))
	if err != nil {
		log.Fatal(err)
	}
	defer dbpool.Close()

	log.Println("Starting session manager")
	sessionManager = scs.New()
	store := pgxstore.New(dbpool)
	sessionManager.Store = store
	sessionManager.Lifetime = 14 * 24 * time.Hour
	defer store.StopCleanup()

	log.Println("Adding routes")
	router := mux.NewRouter()
	router.NotFoundHandler = myNotFoundHandler()
	router.PathPrefix("/static").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	router.HandleFunc("/favicon.ico", faviconHandler)
	router.HandleFunc("/robots.txt", robotsHandler)
	router.HandleFunc("/", indexHandler)

	router.HandleFunc("/legal", legalHandler)
	router.HandleFunc("/about", aboutHandler)
	router.HandleFunc("/login", loginHandler)
	router.HandleFunc("/logout", logoutHandler)
	router.HandleFunc("/register", registerHandler)
	router.HandleFunc("/account", accountHandler)
	router.HandleFunc("/activate", emailconfHandler)
	router.HandleFunc("/oauth/discord", DiscordCallbackHandler)
	router.HandleFunc("/oauth/vk", VKCallbackHandler)

	router.HandleFunc("/hoster", hosterHandler)
	router.HandleFunc("/request", hostRequestHandler)
	router.HandleFunc("/created-rooms", createdRoomsHandler)
	router.HandleFunc("/wzlink", wzlinkHandler)
	router.HandleFunc("/autohoster", autohosterControllHandler)

	router.HandleFunc("/rating", ratingHandler)
	router0 := sessionManager.LoadAndSave(router)
	router1 := handlers.ProxyHeaders(router0)
	router2 := handlers.CompressHandler(router1)
	router3 := handlers.CustomLoggingHandler(os.Stdout, router2, customLogger)
	// router4 := handlers.RecoveryHandler()(router3)
	log.Println("Started!")
	log.Panic(http.ListenAndServe(":"+port, router3))
}