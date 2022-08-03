package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v4"
)

func APItryReachMultihoster(w http.ResponseWriter, r *http.Request) {
	s, m := RequestStatus()
	io.WriteString(w, m)
	if s {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

func APIgetGraphData(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	gid := params["gid"]
	var j string
	derr := dbpool.QueryRow(context.Background(), `SELECT json_agg(frames)::text FROM frames WHERE game = $1`, gid).Scan(&j)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(j)))
	io.WriteString(w, j)
	w.WriteHeader(http.StatusOK)
}

func APIgetDatesGraphData(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	interval := params["interval"]
	var j string
	derr := dbpool.QueryRow(context.Background(), `select
		json_agg(json_build_object(b::text,(select count(*) from games where date_trunc($1, timestarted) = b)))
	from generate_series(date_trunc($1, now() - '1 year 7 days'::interval), now(), $2::interval) as b;`, interval, "1 "+interval).Scan(&j)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, j)
	w.WriteHeader(http.StatusOK)
}

func APIgetDayAverageByHour(w http.ResponseWriter, r *http.Request) {
	rows, derr := dbpool.Query(context.Background(), `select count(gg) as c, extract('hour' from timestarted) as d from games as gg group by d order by d`)
	if derr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	defer rows.Close()
	re := make(map[int]int)
	for rows.Next() {
		var d, c int
		err := rows.Scan(&c, &d)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err.Error())
			return
		}
		re[d] = c
	}
	j, err := json.Marshal(re)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(j))
	w.WriteHeader(http.StatusOK)
}

func APIgetUniquePlayersPerDay(w http.ResponseWriter, r *http.Request) {
	rows, derr := dbpool.Query(context.Background(),
		`SELECT
			b::TEXT,
			(SELECT COUNT(c) FROM
				(SELECT DISTINCT UNNEST(players)
					FROM games
					WHERE date_trunc('day', timestarted) = date_trunc('day', b)) AS c)
		FROM generate_series((select min(timestarted) from games), now(), '1 day'::INTERVAL) AS b;`)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	defer rows.Close()
	re := make(map[string]int)
	for rows.Next() {
		var d string
		var c int
		err := rows.Scan(&d, &c)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err.Error())
			return
		}
		re[d] = c
	}
	j, err := json.Marshal(re)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(j)
	w.WriteHeader(http.StatusOK)
}

func APIgetMapNameCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rows, derr := dbpool.Query(context.Background(), `select mapname, count(*) as c from games group by mapname order by c desc`)
	if derr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	defer rows.Close()
	re := make(map[string]int)
	for rows.Next() {
		var c int
		var n string
		err := rows.Scan(&n, &c)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err.Error())
			return
		}
		re[n] = c
	}
	j, err := json.Marshal(re)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(j))
	w.WriteHeader(http.StatusOK)
}

func APIgetReplayFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !checkUserAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	params := mux.Vars(r)
	gid := params["gid"]
	dir := "0"
	derr := dbpool.QueryRow(context.Background(), `SELECT coalesce(gamedir) FROM games WHERE id = $1;`, gid).Scan(&dir)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	if dir == "-1" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	log.Print(dir)
	replaydir := os.Getenv("MULTIHOSTER_GAMEDIRBASE") + dir + "replay/multiplay/"
	files, err := ioutil.ReadDir(replaydir)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	for _, f := range files {
		// log.Println(f.Name())
		if strings.HasSuffix(f.Name(), ".wzrp") {
			h, err := os.Open(replaydir + f.Name())
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.Print(err.Error())
				return
			}
			var header [4]byte
			n, err := io.ReadFull(h, header[:])
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.Print(err.Error())
				return
			}
			h.Close()
			if n == 4 && string(header[:]) == "WZrp" {
				w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
				w.Header().Set("Content-Disposition", "attachment; filename=\"autohoster-game-"+gid+".wzrp\"")
				http.ServeFile(w, r, replaydir+f.Name())
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func APIgetClassChartGame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	params := mux.Vars(r)
	gid := params["gid"]
	reslog := "0"
	derr := dbpool.QueryRow(context.Background(), `SELECT coalesce(researchlog) FROM games WHERE id = $1;`, gid).Scan(&reslog)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	if reslog == "-1" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	c, err := LoadClassification()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	var resl []resEntry
	err = json.Unmarshal([]byte(reslog), &resl)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	ans, err := json.Marshal(CountClassification(c, resl))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(ans))
	io.WriteString(w, string("\n"))
	w.WriteHeader(http.StatusOK)
}

func APIgetPlayerAllowedJoining(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	params := mux.Vars(r)
	phash := params["hash"]
	badplayed := 0
	derr := dbpool.QueryRow(context.Background(), `SELECT COUNT(id) FROM games WHERE (SELECT id FROM players WHERE hash = $1) = ANY(players) AND gametime < 30000 AND timestarted+'1 day' > now() AND calculated = true;`, phash).Scan(&badplayed)
	if derr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, fmt.Sprint(badplayed))
	log.Println(badplayed)
}

func APIgetAllowedModerators(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rows, derr := dbpool.Query(context.Background(), `select hash from players join users on players.id = users.wzprofile2 where users.allow_preset_request = true;`)
	if derr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	defer rows.Close()
	re := []string{}
	for rows.Next() {
		var h string
		err := rows.Scan(&h)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err.Error())
			return
		}
		re = append(re, h)
	}
	j, err := json.Marshal(re)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(j))
	w.WriteHeader(http.StatusOK)
}

type resEntry struct {
	Name     string  `json:"name"`
	Position float64 `json:"position"`
	Time     float64 `json:"time"`
}

func LoadClassification() (ret []map[string]string, err error) {
	var content []byte
	content, err = os.ReadFile(os.Getenv("CLASSIFICATIONJSON"))
	if err != nil {
		return
	}
	err = json.Unmarshal(content, &ret)
	return
}

// CountClassification in: classification, research out: position[research[time]]
func CountClassification(c []map[string]string, resl []resEntry) (ret map[int]map[string]int) {
	cl := map[string]string{}
	ret = map[int]map[string]int{}
	for _, b := range c {
		cl[b["name"]] = b["Subclass"]
	}
	for _, b := range resl {
		if b.Time < 10 {
			continue
		}
		j, f := cl[b.Name]
		if f {
			_, ff := ret[int(b.Position)]
			if !ff {
				ret[int(b.Position)] = map[string]int{}
			}
			_, ff = ret[int(b.Position)][j]
			if ff {
				ret[int(b.Position)][j]++
			} else {
				ret[int(b.Position)][j] = 1
			}
		}
	}
	return
}

func APIgetClassChartPlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	params := mux.Vars(r)
	pid, err := strconv.Atoi(params["pid"])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	filteri, err := strconv.Atoi(params["category"])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	filter := ""
	filter2 := ""
	if filteri == 1 {
		// filter = " AND array_length(players, 1) = 2 "
	} else if filteri == 2 {
		filter = " AND array_length(players, 1) >= 2 AND alliancetype != 2 "
	} else if filteri == 3 {
		filter2 = " LIMIT 100 "
		// filter = " AND array_length(players, 1) = 2 "
	} else if filteri == 4 {
		filter2 = " LIMIT 100 "
		filter = " AND array_length(players, 1) >= 2 AND alliancetype != 2 "
	}
	rows, derr := dbpool.Query(context.Background(),
		`SELECT coalesce(id, -1), coalesce(researchlog, ''), coalesce(players) 
		FROM games 
		WHERE 
			$1 = any(players) `+filter+`
			AND finished = true 
			AND calculated = true 
			AND hidden = false 
			AND deleted = false 
			AND id > 2000
		ORDER BY id desc
		`+filter2, pid)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	defer rows.Close()
	rowcount := 0
	researches := []string{}
	players := []int{}
	gids := []int{}
	for rows.Next() {
		rowcount++
		var h string
		var p []int
		var gid int
		err := rows.Scan(&gid, &h, &p)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err.Error())
			return
		}
		playerfound := false
		playersreallen := 0
		for i, j := range p {
			if j != -1 {
				playersreallen++
			}
			if j == pid {
				players = append(players, i)
				playerfound = true
				break
			}
		}
		if (filteri == 1 || filteri == 3) && playersreallen > 2 {
			continue
		}
		if !playerfound {
			log.Printf("Can not find player %d in game %d THIS MUST NOT HAPPEN", pid, gid)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		researches = append(researches, h)
		gids = append(gids, gid)
	}
	if rowcount == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	classif, err := LoadClassification()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	ret := map[string]int{}
	for i, j := range researches {
		var resl []resEntry
		err = json.Unmarshal([]byte(j), &resl)
		if err != nil {
			log.Print(err.Error())
			log.Printf("Gid: %d", gids[i])
			log.Print(spew.Sdump(j))
			continue
		}
		cl := CountClassification(classif, resl)
		for v, c := range cl[players[i]] {
			if val, ok := ret[v]; ok {
				ret[v] = val + c
			} else {
				ret[v] = c
			}
		}
	}
	ans, err := json.Marshal(ret)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(ans))
	io.WriteString(w, string("\n"))
	w.WriteHeader(http.StatusOK)
}

func APIgetElodiffChartPlayer(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	pid, err := strconv.Atoi(params["pid"])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rows, derr := dbpool.Query(context.Background(),
		`SELECT
			id,
			coalesce(elodiff, '{0,0,0,0,0,0,0,0,0,0,0}'),
			coalesce(ratingdiff, '{0,0,0,0,0,0,0,0,0,0,0}'),
			to_char(timestarted, 'YYYY-MM-DD HH24:MI'),
			players
		FROM games
		where
			$1 = any(players)
			AND finished = true
			AND calculated = true
			AND hidden = false
			AND deleted = false
			AND id > 200
		order by timestarted asc`, pid)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(derr.Error())
		return
	}
	defer rows.Close()
	type eloHist struct {
		Elo    int
		Rating int
	}
	h := map[string]eloHist{}
	prevts := ""
	for rows.Next() {
		var gid int
		var ediff []int
		var rdiff []int
		var timestarted string
		var players []int
		err := rows.Scan(&gid, &ediff, &rdiff, &timestarted, &players)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err.Error())
			return
		}
		k := -1
		for i, p := range players {
			if p == pid {
				k = i
				break
			}
		}
		if k < 0 || k >= len(ediff) || k >= len(rdiff) {
			log.Printf("Game %d is broken (k %d) players %v diffs %v %v", gid, k, players, ediff, rdiff)
			continue
		}
		eDiff := ediff[k]
		rDiff := rdiff[k]
		if prevts == "" {
			h[timestarted] = eloHist{
				Elo:    1400 + eDiff,
				Rating: 1400 + rDiff,
			}
		} else {
			ph := h[prevts]
			h[timestarted] = eloHist{
				Elo:    ph.Elo + eDiff,
				Rating: ph.Rating + rDiff,
			}
		}
		prevts = timestarted
	}
	ans, err := json.Marshal(h)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(ans))
	io.WriteString(w, string("\n"))
	w.WriteHeader(http.StatusOK)
}

func APIgetLeaderboard(w http.ResponseWriter, r *http.Request) {
	// dbOrder := parseQueryStringFiltered(r, "order", "desc", "asc")
	// dbLimit := parseQueryInt(r, "limit", 5)
	// dbOffset := parseQueryInt(r, "offset", 0)
	// dbOrderBy := parseQueryStringMapped(r, "sort", "elo", map[string]string{
	// 	"Elo2":       "elo2",
	// 	"Autoplayed": "autoplayed",
	// 	"Autowon":    "autowon",
	// 	"Autolost":   "autolost",
	// 	"Name":       "name",
	// 	"ID":         "id",
	// })
	rows, derr := dbpool.Query(context.Background(), `
	SELECT id, name, hash, elo, elo2, autoplayed, autolost, autowon, coalesce((SELECT id FROM users WHERE players.id = users.wzprofile2), -1)
	FROM players
	WHERE autoplayed > 0`)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println("Database query error: " + derr.Error())
		}
		return
	}
	defer rows.Close()
	var P []PlayerLeaderboard
	for rows.Next() {
		var pp PlayerLeaderboard
		rows.Scan(&pp.ID, &pp.Name, &pp.Hash, &pp.Elo, &pp.Elo2, &pp.Autoplayed, &pp.Autolost, &pp.Autowon, &pp.Userid)
		P = append(P, pp)
	}
	ans, err := json.Marshal(P)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	// w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(ans))
	io.WriteString(w, string("\n"))
	w.WriteHeader(http.StatusOK)
}

func APIgetGames(w http.ResponseWriter, r *http.Request) {
	wherecase := "WHERE deleted = false AND hidden = false"
	if sessionGetUsername(r) == "Flex seal" {
		wherecase = ""
	}
	limiter := "LIMIT 5000"
	limiterparam, limiterparamok := r.URL.Query()["all"]
	if limiterparamok && len(limiterparam) >= 1 && limiterparam[0] == "true" {
		limiter = ""
	}
	playerfilter, playerfilterok := r.URL.Query()["player"]
	if playerfilterok && len(playerfilter) >= 1 {
		pid, err := strconv.Atoi(playerfilter[0])
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if wherecase == "" {
			wherecase = fmt.Sprintf("WHERE %d = any(games.players)", pid)
		} else {
			wherecase += fmt.Sprintf(" AND %d = any(games.players)", pid)
		}
	}
	rows, derr := dbpool.Query(context.Background(), `
	SELECT
		games.id as gid, finished, to_char(timestarted, 'YYYY-MM-DD HH24:MI'), coalesce(to_char(timestarted, 'YYYY-MM-DD HH24:MI'), '==='), gametime,
		players, teams, colour, usertype,
		mapname, maphash,
		baselevel, powerlevel, scavs, alliancetype,
		array_agg(to_json(p)::jsonb || json_build_object('userid', coalesce((SELECT id AS userid FROM users WHERE p.id = users.wzprofile2), -1))::jsonb)::text[] as pnames, kills,
		coalesce(elodiff, '{0,0,0,0,0,0,0,0,0,0,0}'), coalesce(ratingdiff, '{0,0,0,0,0,0,0,0,0,0,0}'),
		hidden, calculated, debugtriggered
	FROM games
	JOIN players as p ON p.id = any(games.players)
	`+wherecase+`
	GROUP BY gid
	ORDER BY timestarted DESC
	`+limiter+`;`)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			w.WriteHeader(http.StatusNoContent)
		} else {
			log.Println("Database query error: " + derr.Error())
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	defer rows.Close()
	var gms []DbGamePreview
	for rows.Next() {
		var g DbGamePreview
		var plid []int
		var plteam []int
		var plcolour []int
		var plusertype []string
		var plsj []string
		var dskills []int
		var dselodiff []int
		var dsratingdiff []int
		err := rows.Scan(&g.ID, &g.Finished, &g.TimeStarted, &g.TimeEnded, &g.GameTime,
			&plid, &plteam, &plcolour, &plusertype,
			&g.MapName, &g.MapHash, &g.BaseLevel, &g.PowerLevel, &g.Scavengers, &g.Alliances, &plsj,
			&dskills, &dselodiff, &dsratingdiff, &g.Hidden, &g.Calculated, &g.DebugTriggered)
		if err != nil {
			log.Println("Database scan error: " + err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var np [11]DbGamePlayerPreview
		for pi, pv := range plsj {
			err := json.Unmarshal([]byte(pv), &np[pi])
			if err != nil {
				log.Println("Json unpack error: " + err.Error())
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		for slot, nid := range plid {
			gpi := -1
			for pi, pv := range np {
				if pv.ID == nid {
					gpi = pi
					break
				}
			}
			if gpi == -1 {
				// log.Print("Failed to find player " + strconv.Itoa(slot) + " for game " + strconv.Itoa(g.Id))
				continue
			}
			g.Players[slot] = np[gpi]
			g.Players[slot].Team = plteam[slot]
			g.Players[slot].Colour = plcolour[slot]
			g.Players[slot].Position = slot
			if g.Finished {
				g.Players[slot].Usertype = plusertype[slot]
				g.Players[slot].Kills = dskills[slot]
				if (plusertype[slot] == "winner" || plusertype[slot] == "loser") && len(g.Players) > slot {
					if len(dselodiff) > slot {
						g.Players[slot].EloDiff = dselodiff[slot]
					}
					if len(dsratingdiff) > slot {
						g.Players[slot].RatingDiff = dsratingdiff[slot]
					}
				}
			} else {
				g.Players[slot].Usertype = "fighter"
				g.Players[slot].Kills = 0
			}
		}
		// spew.Dump(g)
		gms = append(gms, g)
	}
	ans, err := json.Marshal(gms)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Print(err.Error())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "https://wz2100-autohost.net https://dev.wz2100-autohost.net")
	// w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, string(ans))
	io.WriteString(w, string("\n"))
	w.WriteHeader(http.StatusOK)
}
