package main

import (
	"anonmail/lib"
	"encoding/json"
	"flag"
	"log"
	"os"
)

func main() {
	configPath := flag.String("cfg", lib.DefaultConfigPath,
		"path to the config file (may be useful to run multiple bots in parallel)")
	flag.Parse()

	configFileBytes, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalln(err)
	}

	var cfg lib.Config
	err = json.Unmarshal(configFileBytes, &cfg)
	if err != nil {
		log.Fatalln(err)
	}

	bot, err := lib.NewBot(cfg)
	if err != nil {
		log.Fatalln(err)
	}

	bot.Start()
}
