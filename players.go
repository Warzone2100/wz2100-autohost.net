package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v4"
)

type PlayerLeaderboard struct {
	ID         int
	Name       string
	Hash       string
	Elo        int
	Elo2       int
	Autoplayed int
	Autolost   int
	Autowon    int
	Userid     int
	Timeplayed int     `json:",omitempty"`
	Rwon       float64 `json:",omitempty"`
	Rlost      float64 `json:",omitempty"`
}

func PlayersHandler(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	pids := params["id"]
	var pid int
	var err error
	if len(pids) == 64 {
		err = dbpool.QueryRow(context.Background(), `SELECT id FROM players WHERE hash = $1`, pids).Scan(&pid)
		if err != nil {
			if err == pgx.ErrNoRows {
				basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msg": "Player not found"})
			} else {
				basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msg": "Error occured"})
			}
			return
		}
	} else {
		var err error
		pid, err = strconv.Atoi(pids)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msg": "Badly formatted player id"})
			return
		}
	}
	var pp PlayerLeaderboard
	pp.ID = pid
	type renameEntry struct {
		Oldname string
		Newname string
		Time    string
	}
	renames := []renameEntry{}
	ChartGamesByPlayercount := newSC("Games by player count", "Game count", "Player count")
	ChartGamesByBaselevel := newSC("Games by base level", "Game count", "Base level")
	ChartGamesByAlliances := newSC("Games by alliance type (2x2+)", "Game count", "Alliance type")
	ChartGamesByScav := newSC("Games by scavengers", "Game count", "Scavengers")
	RatingHistory := map[string]eloHist{}
	ResearchClassificationTotal := map[string]int{}
	ResearchClassificationRecent := map[string]int{}
	err = RequestMultiple(func() error {
		return dbpool.QueryRow(r.Context(), `
		SELECT name, hash, elo, elo2, autoplayed, autolost, autowon, coalesce((SELECT id FROM users WHERE players.id = users.wzprofile2), -1)
		FROM players WHERE id = $1`, pid).Scan(&pp.Name, &pp.Hash, &pp.Elo, &pp.Elo2, &pp.Autoplayed, &pp.Autolost, &pp.Autowon, &pp.Userid)
	}, func() error {
		var o, n, t string
		_, err := dbpool.QueryFunc(r.Context(), `select oldname, newname, "time"::text from plrenames where id = $1 order by "time" desc;`,
			[]interface{}{pid}, []interface{}{&o, &n, &t}, func(qfr pgx.QueryFuncRow) error {
				renames = append(renames, renameEntry{Oldname: o, Newname: n, Time: t})
				return nil
			})
		return err
	}, func() error {
		return dbpool.QueryRow(r.Context(), `select coalesce(avg(p.elo2), 0)
			from games as g
			cross join unnest(g.players) as up
			join players as p on up = p.id
			where
				$1 = any(g.players) and
				calculated = true and
				finished = true and
				g.usertype[array_position(g.players, $1)] = 'winner' and
				g.usertype[array_position(g.players, up)] = 'loser' and
				ratingdiff[1] != 0`, pid).Scan(&pp.Rwon)
	}, func() error {
		return dbpool.QueryRow(r.Context(), `select coalesce(avg(p.elo2), 0)
			from games as g
			cross join unnest(g.players) as up
			join players as p on up = p.id
			where
				$1 = any(g.players) and
				calculated = true and
				finished = true and
				g.usertype[array_position(g.players, $1)] = 'loser' and
				g.usertype[array_position(g.players, up)] = 'winner' and
				ratingdiff[1] != 0`, pid).Scan(&pp.Rlost)
	}, func() error {
		var k, c int
		var ut string
		_, err := dbpool.QueryFunc(r.Context(),
			`select array_position(players, -1)-1 as pc, coalesce(usertype[array_position(players, $1)], '') as ut, count(id)*(array_position(players, -1)-1) as c
			from games
			where
				$1 = any(players) and
				calculated = true and
				finished = true
			group by pc, ut
			order by pc, ut`,
			[]interface{}{pid}, []interface{}{&k, &ut, &c},
			func(_ pgx.QueryFuncRow) error {
				switch ut {
				case "loser":
					ChartGamesByPlayercount.appendToColumn(fmt.Sprintf("%dp", k), "Lost", chartSCcolorLost, c)
				case "winner":
					ChartGamesByPlayercount.appendToColumn(fmt.Sprintf("%dp", k), "Won", chartSCcolorWon, c)
				}
				return nil
			})
		return err
	}, func() error {
		var k, c int
		var ut string
		_, err := dbpool.QueryFunc(r.Context(),
			`select baselevel, usertype[array_position(players, $1)] as ut, count(id)
			from games
			where
				$1 = any(players) and
				calculated = true and
				finished = true
			group by baselevel, ut
			order by baselevel, ut`,
			[]interface{}{pid}, []interface{}{&k, &ut, &c},
			func(_ pgx.QueryFuncRow) error {
				switch ut {
				case "loser":
					ChartGamesByBaselevel.appendToColumn(fmt.Sprintf(`<img class="icons icons-base%d">`, k), "Lost", chartSCcolorLost, c)
				case "winner":
					ChartGamesByBaselevel.appendToColumn(fmt.Sprintf(`<img class="icons icons-base%d">`, k), "Won", chartSCcolorWon, c)
				}
				return nil
			})
		return err
	}, func() error {
		var k, c int
		_, err := dbpool.QueryFunc(r.Context(),
			`select alliancetype, count(id)
			from games
			where
				$1 = any(players) and
				calculated = true and
				finished = true and
				array_position(players, -1)-1 > 2
			group by alliancetype`,
			[]interface{}{pid}, []interface{}{&k, &c},
			func(_ pgx.QueryFuncRow) error {
				if k == 1 {
					return nil
				}
				ChartGamesByAlliances.appendToColumn(fmt.Sprintf(`<img class="icons icons-alliance%d">`, templatesAllianceToClassI(k)), "", chartSCcolorNeutral, c)
				return nil
			})
		return err
	}, func() error {
		var k, c int
		_, err := dbpool.QueryFunc(r.Context(),
			`select scavs::int, count(id)
			from games
			where 
				$1 = any(players) and
				calculated = true and
				finished = true
			group by scavs`,
			[]interface{}{pid}, []interface{}{&k, &c},
			func(_ pgx.QueryFuncRow) error {
				ChartGamesByScav.appendToColumn(fmt.Sprintf(`<img class="icons icons-scav%d">`, k), "", chartSCcolorNeutral, c)
				return nil
			})
		return err
	}, func() error {
		var err error
		ResearchClassificationTotal, ResearchClassificationRecent, err = getPlayerClassifications(pid)
		return err
	}, func() error {
		var err error
		RatingHistory, err = getRatingHistory(pid)
		return err
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msg": "Player not found"})
		} else if r.Context().Err() == context.Canceled {
			return
		} else {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": true, "msg": "Database query error: " + err.Error()})
		}
		return
	}
	basicLayoutLookupRespond("player", w, r, map[string]interface{}{
		"Player":                       pp,
		"Renames":                      renames,
		"ChartGamesByPlayercount":      ChartGamesByPlayercount.calcTotals(),
		"ChartGamesByBaselevel":        ChartGamesByBaselevel.calcTotals(),
		"ChartGamesByAlliances":        ChartGamesByAlliances.calcTotals(),
		"ChartGamesByScav":             ChartGamesByScav.calcTotals(),
		"RatingHistory":                RatingHistory,
		"ResearchClassificationTotal":  ResearchClassificationTotal,
		"ResearchClassificationRecent": ResearchClassificationRecent,
	})
}

type eloHist struct {
	Rating int
}

func getRatingHistory(pid int) (map[string]eloHist, error) {
	rows, derr := dbpool.Query(context.Background(),
		`SELECT
			id,
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
		order by timestarted asc`, pid)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, derr
	}
	defer rows.Close()
	h := map[string]eloHist{}
	prevts := ""
	for rows.Next() {
		var gid int
		var rdiff []int
		var timestarted string
		var players []int
		err := rows.Scan(&gid, &rdiff, &timestarted, &players)
		if err != nil {
			return nil, err
		}
		k := -1
		for i, p := range players {
			if p == pid {
				k = i
				break
			}
		}
		if k < 0 || k >= len(rdiff) {
			log.Printf("Game %d is broken (k %d) players %v diffs %v", gid, k, players, rdiff)
			continue
		}
		rDiff := rdiff[k]
		if prevts == "" {
			h[timestarted] = eloHist{
				Rating: 1400 + rDiff,
			}
		} else {
			ph := h[prevts]
			h[timestarted] = eloHist{
				Rating: ph.Rating + rDiff,
			}
		}
		prevts = timestarted
	}
	return h, nil
}

func APIgetElodiffChartPlayer(_ http.ResponseWriter, r *http.Request) (int, interface{}) {
	params := mux.Vars(r)
	pid, err := strconv.Atoi(params["pid"])
	if err != nil {
		return 400, nil
	}
	h, err := getRatingHistory(pid)
	if err != nil {
		return 500, err
	}
	return 200, h
}
