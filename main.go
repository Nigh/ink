package main

import (
	"bufio"
	"fmt"
	"github.com/InkProject/ink.go"
	"github.com/codegangsta/cli"
	"github.com/go-fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	VERSION      = "Beta (2015-11-08)"
	DEFAULT_ROOT = "blog"
)

var watcher *fsnotify.Watcher
var globalConfig *GlobalConfig
var rootPath string
var conn *websocket.Conn

func main() {
	app := cli.NewApp()
	app.Name = "ink"
	app.Usage = "An elegant static blog generator"
	app.Author = "https://github.com/imeoer"
	app.Email = "imeoer@gmail.com"
	app.Version = VERSION
	app.Commands = []cli.Command{
		{
			Name:  "build",
			Usage: "Generate blog to public folder",
			Action: func(c *cli.Context) {
				ParseGlobalConfigByCli(c, false)
				Build()
			},
		},
		{
			Name:  "preview",
			Usage: "Run in server mode to preview blog",
			Action: func(c *cli.Context) {
				ParseGlobalConfigByCli(c, true)
				Build()
				Watch()
				Server(true)
			},
		},
		{
			Name:  "publish",
			Usage: "Generate blog to public folder and publish",
			Action: func(c *cli.Context) {
				ParseGlobalConfigByCli(c, false)
				Build()
				Publish()
			},
		},
		{
			Name:  "convert",
			Usage: "Convert Jekyll/Hexo post format to Ink format (Beta)",
			Action: func(c *cli.Context) {
				Convert(c)
			},
		},
	}
	app.Run(os.Args)
}

func ParseGlobalConfigByCli(c *cli.Context, develop bool) {
	if len(c.Args()) > 0 {
		rootPath = c.Args()[0]
	} else {
		rootPath = "."
	}
	ParseGlobalConfig(rootPath, develop)
	if globalConfig == nil {
		ParseGlobalConfig(DEFAULT_ROOT, develop)
		if globalConfig == nil {
			Fatal("Parse config.yml failed, please specify a valid path")
		}
	}
}

func ParseGlobalConfig(root string, develop bool) {
	rootPath = root
	globalConfig = ParseConfig(filepath.Join(rootPath, "config.yml"), develop)
	if globalConfig == nil {
		return
	}
}

func Websocket(ctx *ink.Context) {
	// Live reload
	var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	if c, err := upgrader.Upgrade(ctx.Res, ctx.Req, nil); err != nil {
		Fatal(err)
	} else {
		conn = c
	}
	ctx.Stop()
}

func Server(watch bool) {
	port := globalConfig.Build.Port
	if port == "" {
		port = "8000"
	}
	web := ink.New()
	if watch {
		web.Get("/live", Websocket)
	}
	// Static
	web.Get("*", ink.Static(filepath.Join(rootPath, "public")))
	// Listen
	Log("Open http://localhost:" + port + "/ to preview")
	web.Listen("0.0.0.0:" + port)
}

func Watch() {
	// Listen watched file change event
	if watcher != nil {
		watcher.Close()
	}
	watcher, _ = fsnotify.NewWatcher()
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op == fsnotify.Write {
					// Handle when file change
					fmt.Println(event.Name)
					Build()
					if conn != nil {
						if err := conn.WriteMessage(websocket.TextMessage, []byte("change")); err == nil {
							// Fatal(err)
						}
					}
				}
				// case err := <-watcher.Errors:
				// 	Log(err.Error())
			}
		}
	}()
	var dirs = []string{"source"}
	for _, source := range dirs {
		dirPath := filepath.Join(rootPath, source)
		filepath.Walk(dirPath, func(path string, f os.FileInfo, err error) error {
			if f.IsDir() {
				if err := watcher.Add(path); err != nil {
					Log(err.Error())
				}
			}
			return nil
		})
	}
}

func Publish() {
	command := globalConfig.Build.Publish
	// Prepare exec command
	var shell, flag string
	if runtime.GOOS == "windows" {
		shell = "cmd"
		flag = "/C"
	} else {
		shell = "/bin/sh"
		flag = "-c"
	}
	cmd := exec.Command(shell, flag, command)
	cmd.Dir = filepath.Join(rootPath, "public")
	// Start print stdout and stderr of process
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	out := bufio.NewScanner(stdout)
	err := bufio.NewScanner(stderr)
	// Print stdout
	go func() {
		for out.Scan() {
			Log(out.Text())
		}
	}()
	// Print stdin
	go func() {
		for err.Scan() {
			Log(err.Text())
		}
	}()
	// Exec command
	cmd.Run()
}

func Convert(c *cli.Context) {
	// Parse arguments
	var sourcePath, rootPath string
	args := c.Args()
	if len(args) > 0 {
		sourcePath = args[0]
	} else {
		Fatal("Please specify the posts source path")
	}
	if len(args) > 1 {
		rootPath = args[1]
	} else {
		rootPath = "."
	}
	// Check if path exist
	if !Exists(sourcePath) || !Exists(rootPath) {
		Fatal("Please specify valid path")
	}
	// Parse Jekyll/Hexo post file
	count := 0
	filepath.Walk(sourcePath, func(path string, f os.FileInfo, err error) error {
		fileExt := strings.ToLower(filepath.Ext(path))
		if fileExt == ".md" || fileExt == ".html" {
			// Read data from file
			data, err := ioutil.ReadFile(path)
			fileName := filepath.Base(path)
			Log("Converting " + fileName)
			if err != nil {
				Fatal(err.Error())
			}
			// Split config and markdown
			var configStr, contentStr string
			content := strings.TrimSpace(string(data))
			parseAry := strings.SplitN(content, "---", 3)
			parseLen := len(parseAry)
			if parseLen == 3 { // Jekyll
				configStr = parseAry[1]
				contentStr = parseAry[2]
			} else if parseLen == 2 { // Hexo
				configStr = parseAry[0]
				contentStr = parseAry[1]
			}
			// Parse config
			var article ArticleConfig
			if err = yaml.Unmarshal([]byte(configStr), &article); err != nil {
				Fatal(err.Error())
			}
			tags := make(map[string]bool)
			for _, t := range article.Tags {
				tags[t] = true
			}
			for _, c := range article.Categories {
				if _, ok := tags[c]; !ok {
					article.Tags = append(article.Tags, c)
				}
			}
			if article.Author == "" {
				article.Author = "me"
			}
			// Convert date
			dateAry := strings.SplitN(article.Date, ".", 2)
			if len(dateAry) == 2 {
				article.Date = dateAry[0]
			}
			if len(article.Date) == 10 {
				article.Date = article.Date + " 00:00:00"
			}
			if len(article.Date) == 0 {
				article.Date = "1970-01-01 00:00:00"
			}
			article.Update = ""
			// Generate Config
			var inkConfig []byte
			if inkConfig, err = yaml.Marshal(article); err != nil {
				Fatal(err.Error())
			}
			inkConfigStr := string(inkConfig)
			markdownStr := inkConfigStr + "\n\n---\n\n" + contentStr + "\n"
			targetName := "source/" + fileName
			if fileExt != ".md" {
				targetName = targetName + ".md"
			}
			ioutil.WriteFile(filepath.Join(rootPath, targetName), []byte(markdownStr), 0644)
			count++
		}
		return nil
	})
	fmt.Printf("\nConvert finish, total %v articles\n", count)
}
