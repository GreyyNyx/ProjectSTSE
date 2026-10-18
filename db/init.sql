-- Skema + seed database sepak bola (PostgreSQL)
-- Dijalankan otomatis oleh image postgres saat volume masih kosong.

CREATE EXTENSION IF NOT EXISTS pg_trgm;  -- mempercepat pencarian nama (ILIKE '%kata%')

CREATE TABLE teams (
    id   SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE tournaments (
    id   SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE matches (
    id            SERIAL PRIMARY KEY,
    match_date    DATE    NOT NULL,
    home_team_id  INT     NOT NULL REFERENCES teams(id),
    away_team_id  INT     NOT NULL REFERENCES teams(id),
    home_score    INT     NOT NULL,
    away_score    INT     NOT NULL,
    tournament_id INT     NOT NULL REFERENCES tournaments(id),
    city          TEXT,
    country       TEXT,
    neutral       BOOLEAN NOT NULL DEFAULT FALSE
);

-- Tabel sementara untuk membaca CSV apa adanya
CREATE TEMP TABLE raw_matches (
    date       DATE,
    home_team  TEXT,
    away_team  TEXT,
    home_score INT,
    away_score INT,
    tournament TEXT,
    city       TEXT,
    country    TEXT,
    neutral    BOOLEAN
);

COPY raw_matches FROM '/docker-entrypoint-initdb.d/results.csv' WITH (FORMAT csv, HEADER true);

INSERT INTO teams (name)
SELECT DISTINCT name FROM (
    SELECT home_team AS name FROM raw_matches
    UNION
    SELECT away_team FROM raw_matches
) t ORDER BY name;

INSERT INTO tournaments (name)
SELECT DISTINCT tournament FROM raw_matches ORDER BY tournament;

INSERT INTO matches (match_date, home_team_id, away_team_id, home_score, away_score,
                     tournament_id, city, country, neutral)
SELECT r.date, h.id, a.id, r.home_score, r.away_score, t.id, r.city, r.country, r.neutral
FROM raw_matches r
JOIN teams h       ON h.name = r.home_team
JOIN teams a       ON a.name = r.away_team
JOIN tournaments t ON t.name = r.tournament
ORDER BY r.date, r.home_team, r.away_team;

DROP TABLE raw_matches;

-- Index untuk filter, sorting, dan pencarian
CREATE INDEX idx_matches_date       ON matches (match_date DESC);
CREATE INDEX idx_matches_home       ON matches (home_team_id);
CREATE INDEX idx_matches_away       ON matches (away_team_id);
CREATE INDEX idx_matches_tournament ON matches (tournament_id);
CREATE INDEX idx_teams_name_trgm    ON teams       USING gin (name gin_trgm_ops);
CREATE INDEX idx_tourn_name_trgm    ON tournaments USING gin (name gin_trgm_ops);

-- View praktis: pertandingan dengan nama tim & turnamen (dipakai endpoint list/detail)
CREATE VIEW v_matches AS
SELECT m.id, m.match_date, EXTRACT(YEAR FROM m.match_date)::INT AS year,
       h.id AS home_team_id, h.name AS home_team,
       a.id AS away_team_id, a.name AS away_team,
       m.home_score, m.away_score,
       t.id AS tournament_id, t.name AS tournament,
       m.city, m.country, m.neutral
FROM matches m
JOIN teams h       ON h.id = m.home_team_id
JOIN teams a       ON a.id = m.away_team_id
JOIN tournaments t ON t.id = m.tournament_id;

-- View klasemen per turnamen per tahun (3 poin menang, 1 seri)
CREATE VIEW v_standings AS
WITH r AS (
    SELECT tournament_id, EXTRACT(YEAR FROM match_date)::INT AS year,
           home_team_id AS team_id, home_score AS gf, away_score AS ga FROM matches
    UNION ALL
    SELECT tournament_id, EXTRACT(YEAR FROM match_date)::INT,
           away_team_id, away_score, home_score FROM matches
)
SELECT r.tournament_id, r.year, r.team_id, tm.name AS team,
       COUNT(*)                                   AS played,
       COUNT(*) FILTER (WHERE gf > ga)            AS won,
       COUNT(*) FILTER (WHERE gf = ga)            AS drawn,
       COUNT(*) FILTER (WHERE gf < ga)            AS lost,
       SUM(gf)                                    AS goals_for,
       SUM(ga)                                    AS goals_against,
       SUM(gf) - SUM(ga)                          AS goal_diff,
       3 * COUNT(*) FILTER (WHERE gf > ga)
         + COUNT(*) FILTER (WHERE gf = ga)        AS points
FROM r JOIN teams tm ON tm.id = r.team_id
GROUP BY r.tournament_id, r.year, r.team_id, tm.name;
