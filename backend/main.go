package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type server struct{ db *sql.DB }


func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverErr(w http.ResponseWriter, err error) {
	log.Println("error:", err)
	writeErr(w, http.StatusInternalServerError, "terjadi kesalahan pada server")
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func paging(r *http.Request) (page, limit, offset int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit, (page - 1) * limit
}

type filter struct {
	conds []string
	args  []any
}

func (f *filter) add(format string, val any) {
	f.args = append(f.args, val)
	n := len(f.args)
	f.conds = append(f.conds, fmt.Sprintf(format, n))
}

func (f *filter) where() string {
	if len(f.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.conds, " AND ")
}


type Match struct {
	ID           int    `json:"id"`
	MatchDate    string `json:"match_date"`
	Year         int    `json:"year"`
	HomeTeamID   int    `json:"home_team_id"`
	HomeTeam     string `json:"home_team"`
	AwayTeamID   int    `json:"away_team_id"`
	AwayTeam     string `json:"away_team"`
	HomeScore    int    `json:"home_score"`
	AwayScore    int    `json:"away_score"`
	TournamentID int    `json:"tournament_id"`
	Tournament   string `json:"tournament"`
	City         string `json:"city"`
	Country      string `json:"country"`
	Neutral      bool   `json:"neutral"`
}

const matchCols = `id, match_date::text, year, home_team_id, home_team, away_team_id, away_team,
	home_score, away_score, tournament_id, tournament, COALESCE(city,''), COALESCE(country,''), neutral`

func scanMatches(rows *sql.Rows) ([]Match, error) {
	defer rows.Close()
	out := []Match{}
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.MatchDate, &m.Year, &m.HomeTeamID, &m.HomeTeam, &m.AwayTeamID,
			&m.AwayTeam, &m.HomeScore, &m.AwayScore, &m.TournamentID, &m.Tournament, &m.City,
			&m.Country, &m.Neutral); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}


func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database tidak terhubung")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) listMatches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, limit, offset := paging(r)

	var f filter
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		f.add("(home_team ILIKE $%[1]d OR away_team ILIKE $%[1]d OR tournament ILIKE $%[1]d OR city ILIKE $%[1]d)", "%"+v+"%")
	}
	if v, err := strconv.Atoi(q.Get("tournament_id")); err == nil {
		f.add("tournament_id = $%d", v)
	}
	if v, err := strconv.Atoi(q.Get("team_id")); err == nil {
		f.add("(home_team_id = $%[1]d OR away_team_id = $%[1]d)", v)
	}
	if v, err := strconv.Atoi(q.Get("year")); err == nil {
		f.add("year = $%d", v)
	}

	sortCols := map[string]string{
		"date": "match_date", "home_team": "home_team", "away_team": "away_team",
		"tournament": "tournament", "home_score": "home_score", "away_score": "away_score",
	}
	col, ok := sortCols[q.Get("sort")]
	if !ok {
		col = "match_date"
	}
	order := "DESC"
	if strings.EqualFold(q.Get("order"), "asc") {
		order = "ASC"
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM v_matches"+f.where(), f.args...).Scan(&total); err != nil {
		serverErr(w, err)
		return
	}

	args := append(append([]any{}, f.args...), limit, offset)
	sqlStr := fmt.Sprintf("SELECT %s FROM v_matches%s ORDER BY %s %s, id %s LIMIT $%d OFFSET $%d",
		matchCols, f.where(), col, order, order, len(f.args)+1, len(f.args)+2)
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	data, err := scanMatches(rows)
	if err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": page, "limit": limit, "total": total,
		"total_pages": (total + limit - 1) / limit, "data": data,
	})
}

func (s *server) getMatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	rows, err := s.db.Query("SELECT "+matchCols+" FROM v_matches WHERE id = $1", id)
	if err != nil {
		serverErr(w, err)
		return
	}
	data, err := scanMatches(rows)
	if err != nil {
		serverErr(w, err)
		return
	}
	if len(data) == 0 {
		writeErr(w, http.StatusNotFound, "pertandingan tidak ditemukan")
		return
	}
	writeJSON(w, http.StatusOK, data[0])
}

func (s *server) listTeams(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, limit, offset := paging(r)

	var f filter
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		f.add("name ILIKE $%d", "%"+v+"%")
	}
	order := "ASC"
	if strings.EqualFold(q.Get("order"), "desc") {
		order = "DESC"
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM teams"+f.where(), f.args...).Scan(&total); err != nil {
		serverErr(w, err)
		return
	}
	args := append(append([]any{}, f.args...), limit, offset)
	rows, err := s.db.Query(fmt.Sprintf("SELECT id, name FROM teams%s ORDER BY name %s LIMIT $%d OFFSET $%d",
		f.where(), order, len(f.args)+1, len(f.args)+2), args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()
	type team struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	data := []team{}
	for rows.Next() {
		var t team
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			serverErr(w, err)
			return
		}
		data = append(data, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": page, "limit": limit, "total": total,
		"total_pages": (total + limit - 1) / limit, "data": data,
	})
}

func (s *server) getTeam(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	var name string
	err = s.db.QueryRow("SELECT name FROM teams WHERE id = $1", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "tim tidak ditemukan")
		return
	}
	if err != nil {
		serverErr(w, err)
		return
	}

	var played, won, drawn, lost, gf, ga int
	err = s.db.QueryRow(`
		WITH r AS (
			SELECT home_score AS gf, away_score AS ga FROM matches WHERE home_team_id = $1
			UNION ALL
			SELECT away_score, home_score FROM matches WHERE away_team_id = $1
		)
		SELECT COUNT(*), COUNT(*) FILTER (WHERE gf > ga), COUNT(*) FILTER (WHERE gf = ga),
		       COUNT(*) FILTER (WHERE gf < ga), COALESCE(SUM(gf),0), COALESCE(SUM(ga),0) FROM r`, id).
		Scan(&played, &won, &drawn, &lost, &gf, &ga)
	if err != nil {
		serverErr(w, err)
		return
	}

	rows, err := s.db.Query("SELECT "+matchCols+` FROM v_matches
		WHERE home_team_id = $1 OR away_team_id = $1 ORDER BY match_date DESC, id DESC LIMIT 10`, id)
	if err != nil {
		serverErr(w, err)
		return
	}
	recent, err := scanMatches(rows)
	if err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "name": name,
		"stats": map[string]int{"played": played, "won": won, "drawn": drawn, "lost": lost,
			"goals_for": gf, "goals_against": ga},
		"recent_matches": recent,
	})
}

func (s *server) listTournaments(w http.ResponseWriter, r *http.Request) {
	var f filter
	if v := strings.TrimSpace(r.URL.Query().Get("search")); v != "" {
		f.add("name ILIKE $%d", "%"+v+"%")
	}
	rows, err := s.db.Query("SELECT id, name FROM tournaments"+f.where()+" ORDER BY name", f.args...)
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()
	type tournament struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	data := []tournament{}
	for rows.Next() {
		var t tournament
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			serverErr(w, err)
			return
		}
		data = append(data, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(data), "data": data})
}

func (s *server) listYears(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT DISTINCT year FROM v_matches ORDER BY year DESC")
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()
	data := []int{}
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			serverErr(w, err)
			return
		}
		data = append(data, y)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *server) standings(w http.ResponseWriter, r *http.Request) {
	tid, err1 := strconv.Atoi(r.URL.Query().Get("tournament_id"))
	year, err2 := strconv.Atoi(r.URL.Query().Get("year"))
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "parameter tournament_id dan year wajib diisi")
		return
	}
	rows, err := s.db.Query(`
		SELECT team_id, team, played, won, drawn, lost, goals_for, goals_against, goal_diff, points
		FROM v_standings WHERE tournament_id = $1 AND year = $2
		ORDER BY points DESC, goal_diff DESC, goals_for DESC, team`, tid, year)
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Position     int    `json:"position"`
		TeamID       int    `json:"team_id"`
		Team         string `json:"team"`
		Played       int    `json:"played"`
		Won          int    `json:"won"`
		Drawn        int    `json:"drawn"`
		Lost         int    `json:"lost"`
		GoalsFor     int    `json:"goals_for"`
		GoalsAgainst int    `json:"goals_against"`
		GoalDiff     int    `json:"goal_diff"`
		Points       int    `json:"points"`
	}
	data := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.TeamID, &x.Team, &x.Played, &x.Won, &x.Drawn, &x.Lost,
			&x.GoalsFor, &x.GoalsAgainst, &x.GoalDiff, &x.Points); err != nil {
			serverErr(w, err)
			return
		}
		x.Position = len(data) + 1
		data = append(data, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tournament_id": tid, "year": year, "data": data})
}


func main() {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		getenv("DB_HOST", "localhost"), getenv("DB_PORT", "5432"),
		os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	for i := 1; i <= 30; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		log.Printf("menunggu database (%d/30): %v", i, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatal("database tidak bisa dihubungi: ", err)
	}

	s := &server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/matches", s.listMatches)
	mux.HandleFunc("GET /api/matches/{id}", s.getMatch)
	mux.HandleFunc("GET /api/teams", s.listTeams)
	mux.HandleFunc("GET /api/teams/{id}", s.getTeam)
	mux.HandleFunc("GET /api/tournaments", s.listTournaments)
	mux.HandleFunc("GET /api/years", s.listYears)
	mux.HandleFunc("GET /api/standings", s.standings)

	addr := ":" + getenv("PORT", "8080")
	log.Println("API berjalan di", addr)
	log.Fatal(http.ListenAndServe(addr, cors(mux)))
}
