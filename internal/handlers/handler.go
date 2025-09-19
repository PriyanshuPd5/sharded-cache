package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/yourusername/sharded-cache/internal/cache"
)


type setRequest struct {
	Key string `json:"key"`
	Val string `json:"value"`
	TTL int    `json:"ttl"` // seconds
}

func MakeSetHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		ttl := time.Duration(req.TTL) * time.Second
		_, _ = c.Set(req.Key, []byte(req.Val), ttl)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func MakeGetHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		key := vars["key"]
		val, err := c.Get(key)
		if err != nil {
			if errors.Is(err, cache.ErrNotFound) {
				httpErr(w, http.StatusNotFound, "not found")
			} else {
				httpErr(w, http.StatusInternalServerError, "error")
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": string(val)})
	}
}

func MakeDelHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		key := vars["key"]
		_, err := c.Delete(key)
		if err != nil {
			if errors.Is(err, cache.ErrNotFound) {
				httpErr(w, http.StatusNotFound, "not found")
			} else {
				httpErr(w, http.StatusInternalServerError, "error")
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func MakeKeysHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("pattern")
		if p == "" {
			p = "*"
		}
		keys, err := c.Keys(p)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys, "count": len(keys)})
	}
}

func MakeScanHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		curStr := r.URL.Query().Get("cursor")
		match := r.URL.Query().Get("match")
		countStr := r.URL.Query().Get("count")

		var cursor uint64
		if curStr != "" {
			if v, err := strconv.ParseUint(curStr, 10, 64); err == nil {
				cursor = v
			}
		}
		count := 100
		if countStr != "" {
			if v, err := strconv.Atoi(countStr); err == nil && v > 0 {
				count = v
			}
		}
		next, keys, err := c.Scan(cursor, match, count)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"cursor": next, "keys": keys})
	}
}

func MakeFlushHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.FlushAll()
		writeJSON(w, http.StatusOK, map[string]string{"status": "flushed"})
	}
}

func MakeStatsHandler(c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type nodeInfo struct {
			Index int   `json:"index"`
			Keys  int64 `json:"keys"`
		}
		nodes := c.Nodes()
		out := make([]nodeInfo, 0, len(nodes))
		total := int64(0)
		for i, n := range nodes {
			cnt := n.KeyCount()
			out = append(out, nodeInfo{Index: i, Keys: cnt})
			total += cnt
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"total": total, "nodes": out})
	}
}