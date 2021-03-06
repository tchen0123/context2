package data

import (
	"../../common"
	"../config"
	"../event"
	"bufio"
	"fmt"
	"github.com/mxk/go-sqlite/sqlite3"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Data struct {
	databaseFile string
	config    config.Config
	Data      []event.Event
	Bookmarks []Bookmark
	Summary   []int
	Threads   []string
	VisibleThreadIDs []int
	LogStart  float64
	LogEnd    float64
	setStatusCB func(string)
}

func splitExt(path string) (root, ext string) {
	ext = filepath.Ext(path)
	root = path[:len(path)-len(ext)]
	return
}

func VersionCheck(databaseFile string) bool {
	//self.setStatus("Checking database version")
	db, _ := sqlite3.Open(databaseFile)
	defer db.Close()

	query, err := db.Query("SELECT version FROM settings LIMIT 1")
	if err != nil {
		fmt.Printf("Error getting version query: %s\n", err)
		return false
	}

	var version int
	err = query.Scan(&version)
	if err != nil {
		fmt.Printf("Error getting version row: %s\n", err)
		return false
	}
	if version != common.DB_VER {
		fmt.Printf("Incompatible binary version: %d != %d\n", version, common.DB_VER)
		return false
	}

	return true
}

func (self *Data) SetStatusCB(setStatus func(string)) {
	self.setStatusCB = setStatus
}

func (self *Data) setStatus(status string) {
	if self.setStatusCB != nil {
		self.setStatusCB(status)
	}
}

func (self *Data) OpenFile(givenFile string, config config.Config) (string, error) {
	self.setStatus(fmt.Sprintf("Loading: file %s", givenFile))

	self.config = config

	path, _ := splitExt(givenFile)
	logFile := path + ".ctxt"
	databaseFile := path + ".cbin"

	// if the user picked a log file, compile it (unless an
	// up-to-date version already exists)
	if givenFile == logFile {
		needsRecompile := false

		logStat, err := os.Stat(logFile)
		if err != nil {
			return "", err
		}

		databaseStat, err := os.Stat(databaseFile)

		if err != nil {
			needsRecompile = true
			self.setStatus("Compiled log not found, compiling")
		} else if logStat.ModTime().UnixNano() > databaseStat.ModTime().UnixNano() {
			needsRecompile = true
			self.setStatus("Compiled log is out of date, recompiling")
		} else if !VersionCheck(databaseFile) {
			needsRecompile = true
			self.setStatus("Compiled log is from an old version of context, recompiling")
		}

		if needsRecompile {
			self.setStatus("Recompiling")
			compiler := exec.Command("context-compiler", logFile)
			pipe, _ := compiler.StdoutPipe()
			reader := bufio.NewScanner(pipe)
			compiler.Start()

			for reader.Scan() {
				line := reader.Text()
				if line != "" {
					self.setStatus(strings.Trim(line, "\n\r"))
				} else {
					break
				}
			}
		}
	}

	self.databaseFile = databaseFile

	// self.LoadEvents()
	self.Data = []event.Event{}

	// self.LoadBookmarks()
	self.Bookmarks = []Bookmark{}

	// self.LoadSettings()
	self.LogStart = 0
	self.LogEnd = 0

	// self.LoadThreads()
	self.Threads = make([]string, 0, 10)

	return databaseFile, nil
}

func (self *Data) LoadEvents(renderStart, renderLen, coalesce, cutoff float64) {
	self.setStatus("Loading: events")

	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	self.setStatus("Loading...")
	defer self.setStatus("")
	s := renderStart
	e := renderStart + renderLen

	// free memory
	self.Data = []event.Event{}
	self.VisibleThreadIDs = []int{}

	// private copy of new data, to avoid concurrent LoadEvents calls messing with each other
	newData := []event.Event{}
	newVisibleThreadIDs := []int{}
	threadStacks := make([][]int, len(self.Threads))

	/*
			n = 0
		   	func progress() {
		   		n++
		   		setStatus("Loading... (%dk opcodes)" % (self.n * 10))
		   		return 0  // non-zero = cancel query
		   	}
		   	self.c.set_progress_handler(progress, 10000)
		       defer self.c.set_progress_handler(None, 0)
	*/

	sql := `
		SELECT *
		FROM events
		WHERE id IN (SELECT id FROM events_index WHERE end_time > ? AND start_time < ?)
		AND (
			(end_time - start_time) >= ? OR
			start_type = "BMARK"
		)
		ORDER BY start_time ASC, end_time DESC
	`
	for query, err := conn.Query(sql, s-self.LogStart, e-self.LogStart, cutoff); err == nil; err = query.Next() {
		var evt event.Event

		// load the basic 1:1 data
		evt.NewEvent()
		query.Scan(
			&evt.ID,
			&evt.ThreadID,
			&evt.StartLocation, &evt.EndLocation,
			&evt.StartTime, &evt.EndTime,
			&evt.StartType, &evt.EndType,
			&evt.StartText, &evt.EndText,
		)

		// calculate thread Index
		evt.ThreadIndex = -1
		i := 0
		for ; i<len(newVisibleThreadIDs); i++ {
			if newVisibleThreadIDs[i] == evt.ThreadID {
				evt.ThreadIndex = i
			}
		}
		if evt.ThreadIndex == -1 {
			newVisibleThreadIDs = append(newVisibleThreadIDs, evt.ThreadID)
			evt.ThreadIndex = i
		}

		// load data, coalescing if appropriate
		if evt.StartType == "START" {
			prevEventAtLevel := -1

			for {
				endIdx := len(threadStacks[evt.ThreadIndex]) - 1
				if endIdx < 0 || newData[threadStacks[evt.ThreadIndex][endIdx]].EndTime > evt.StartTime {
					break
				}
				prevEventAtLevel = threadStacks[evt.ThreadIndex][endIdx]
				threadStacks[evt.ThreadIndex] = threadStacks[evt.ThreadIndex][:endIdx]
			}
			evt.Depth = len(threadStacks[evt.ThreadIndex])

			if coalesce > 0.0 &&
				prevEventAtLevel != -1 &&
				newData[prevEventAtLevel].CanMerge(evt, coalesce) {
				// previous event is still most recent at this stack level, put it back
				threadStacks[evt.ThreadIndex] = append(threadStacks[evt.ThreadIndex], prevEventAtLevel)
				newData[prevEventAtLevel].Merge(evt)
			} else {
				// a new event is added to the stack
				threadStacks[evt.ThreadIndex] = append(threadStacks[evt.ThreadIndex], len(newData))
				newData = append(newData, evt)
			}
		} else {
			newData = append(newData, evt)
		}
	}

	self.setStatus("Sorting events")
	sort.Sort(event.ByType(newData))

	self.Data = newData
	self.VisibleThreadIDs = newVisibleThreadIDs
	
	self.setStatus("Loading: done")
}

func (self *Data) LoadBookmarks() {
	self.setStatus("Loading: bookmarks")

	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	n := 0
	newBookmarks := []Bookmark{}

	sql := "SELECT start_time, start_text, end_text FROM events WHERE start_type = 'BMARK' ORDER BY start_time"
	for query, err := conn.Query(sql); err == nil; err = query.Next() {
		if n%1000 == 0 {
			self.setStatus(fmt.Sprintf("Loaded %d bookmarks", n))
		}
		n++
		var startTime float64
		var startText, endText string
		query.Scan(&startTime, &startText, &endText)

		newBookmarks = append(newBookmarks, Bookmark{startTime, startText})
	}

	self.Bookmarks = newBookmarks
}

func (self *Data) LoadSettings() {
	self.setStatus("Loading: settings")

	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	sql := "SELECT start_time, end_time FROM settings"
	for query, err := conn.Query(sql); err == nil; err = query.Next() {
		query.Scan(&self.LogStart, &self.LogEnd)
	}
}

func (self *Data) LoadThreads() {
	self.setStatus("Loading: threads")

	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	newThreads := make([]string, 0, 0)

	sql := "SELECT node, process, thread FROM threads ORDER BY id"
	for query, err := conn.Query(sql); err == nil; err = query.Next() {
		var node, process, thread string
		query.Scan(&node, &process, &thread)
		newThreads = append(newThreads, fmt.Sprintf("%s-%s-%s", node, process, thread))
	}

	self.Threads = newThreads
}

func (self *Data) LoadSummary() {
	self.setStatus("Loading: summary")

	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	newSummary := make([]int, 0, 1000)

	sql := "SELECT events FROM summary ORDER BY id"
	for query, err := conn.Query(sql); err == nil; err = query.Next() {
		var val int
		query.Scan(&val)
		newSummary = append(newSummary, val)
	}

	self.Summary = newSummary
}

func (self *Data) GetEarliestBookmarkAfter(startHint float64) float64 {
	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	var startTime float64
	sql := "SELECT min(start_time) FROM events WHERE start_time > ? AND start_type = 'BMARK'"
	for query, err := conn.Query(sql, startHint); err == nil; err = query.Next() {
		query.Scan(&startTime)
	}
	return startTime
}

func (self *Data) GetLatestBookmarkBefore(endHint float64) float64 {
	conn, _ := sqlite3.Open(self.databaseFile)
	defer conn.Close()

	var endTime float64
	sql := "SELECT max(start_time) FROM events WHERE start_time < ? AND start_type = 'BMARK'"
	for query, err := conn.Query(sql, endHint); err == nil; err = query.Next() {
		query.Scan(&endTime)
	}
	return endTime
}
