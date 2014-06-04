package viewer

import (
	"encoding/json"
	"log"
	"os"
	"os/user"
)

type Config struct {
	Render struct {
		Start    float64
		Length   float64
		Scale    float64
		MaxDepth int
		Cutoff   float64
		Coalesce float64
		Bookmarks bool
	}
	Gui struct {
		RenderAuto bool
		LastLogDir string
	}
	Bookmarks struct {
		Absolute bool
		Format   string
	}
}

func (self *Config) Default() {
	usr, _ := user.Current()

	self.Render.Start = 0
	self.Render.Length = 20.0
	self.Render.Scale = 50.0
	self.Render.MaxDepth = 7
	self.Render.Cutoff = 0.0
	self.Render.Coalesce = 0.0
	self.Render.Bookmarks = false

	self.Gui.RenderAuto = true
	self.Gui.LastLogDir = usr.HomeDir

	self.Bookmarks.Absolute = true
	self.Bookmarks.Format = "2006/01/02 15:04:05"
}

func (self *Config) Load(configFile string) {
	buf := make([]byte, 2048)

	fp, err := os.Open(configFile)
	if err != nil {
		log.Printf("Error loading settings from %s: %s\n", configFile, err)
		self.Default()
		return
	}

	_, err = fp.Read(buf)
	if err != nil {
		log.Printf("Error loading settings from %s: %s\n", configFile, err)
		self.Default()
		return
	}

	err = json.Unmarshal(buf, self)
	if err != nil {
		log.Printf("Error loading settings from %s: %s\n", configFile, err)
		self.Default()
		return
	}
}

func (self *Config) Save(configFile string) {
	fp, err := os.Create(configFile)
	if err != nil {
		log.Printf("Error saving settings to %s: %s\n", configFile, err)
		return
	}

	b, err := json.MarshalIndent(self, "", "    ")
	if err != nil {
		log.Printf("Error saving settings to %s: %s\n", configFile, err)
		return
	}

	_, err = fp.Write(b)
	if err != nil {
		log.Printf("Error saving settings to %s: %s\n", configFile, err)
		return
	}
}
