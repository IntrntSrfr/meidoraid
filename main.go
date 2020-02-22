package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/time/rate"
)

type Config struct {
	Token string `json:"token"`
	Owner string `json:"owner"`
}

var (
	servers = serverMap{servers: make(map[string]*server)}
	config  Config
)

func main() {
	f, err := ioutil.ReadFile("./config.json")
	if err != nil {
		fmt.Println(err)
		return
	}

	var config Config
	json.Unmarshal(f, &config)

	client, err := discordgo.New("Bot " + config.Token)
	if err != nil {
		fmt.Println(err)
		return
	}

	go servers.runCleaner()

	addHandlers(client)

	err = client.Open()
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	client.Close()
}

func addHandlers(s *discordgo.Session) {

	s.AddHandler(ReadyHandler)
	s.AddHandler(DisconnectHandler)

	s.AddHandler(GuildCreateHandler)
	s.AddHandler(GuildUnavailableHandler)

	s.AddHandler(GuildMemberAddHandler)
	s.AddHandler(RaidToggleHandler)
	s.AddHandler(MessageCreateHandler)
}

func ReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	fmt.Println(fmt.Sprintf("Logged in as %v.", r.User.String()))
}

func DisconnectHandler(s *discordgo.Session, d *discordgo.Disconnect) {
	fmt.Println("Disconnected at: " + time.Now().String())
}

func GuildCreateHandler(s *discordgo.Session, g *discordgo.GuildCreate) {
	servers.Add(g.ID)
}

func GuildUnavailableHandler(s *discordgo.Session, g *discordgo.GuildDelete) {
	servers.Remove(g.ID)
}

func GuildMemberAddHandler(s *discordgo.Session, m *discordgo.GuildMemberAdd) {

	srv, ok := servers.Get(m.GuildID)
	if !ok {
		return
	}

	srv.AddToJoinCache(m.User.ID)

	if !srv.RaidMode() {
		return
	}

	if isNewAccount(m.User.ID) {
		fmt.Println("new account")
		//s.GuildBanCreateWithReason(m.GuildID, m.User.ID, "Raid measure", 7)
	}
}

func RaidToggleHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.Bot {
		return
	}

	srv, ok := servers.Get(m.GuildID)
	if !ok {
		return
	}

	if strings.HasPrefix(strings.ToLower(m.Content), "m?raidmode") {
		srv.RaidToggle()
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("raid mode set to %v", srv.RaidMode()))
	}
}

func MessageCreateHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	srv, ok := servers.Get(m.GuildID)
	if !ok {
		return
	}

	if !srv.RaidMode() {
		return
	}

	usr, ok := srv.GetUser(m.Author.ID)
	if !ok {
		srv.Add(m.Author.ID)
		return
	}

	if !usr.Allow() || len(m.Mentions) > 10 {
		// ban the user
		fmt.Println("bad user")
	}
}

func isNewAccount(userID string) bool {

	id, err := strconv.ParseInt(userID, 0, 63)
	if err != nil {
		return false
	}

	id = ((id >> 22) + 1420070400000) / 1000

	// how long time should be acceptable, currently set to 2 days
	threshold := time.Now().Add(-1 * time.Hour * 24 * 2)

	ts := time.Unix(id, 0)

	if ts.Unix() > threshold.Unix() {
		return true
	}
	return false
}

func hasRole() bool {
	return false
}

type serverMap struct {
	sync.RWMutex
	servers map[string]*server
}

func (s *serverMap) Add(id string) {
	s.Lock()
	defer s.Unlock()
	s.servers[id] = &server{
		raidMode: false,
		users:    make(map[string]*rate.Limiter),
		lastRaid: []string{},
	}
}
func (s *serverMap) Remove(id string) {
	s.Lock()
	defer s.Unlock()
	delete(s.servers, id)
}
func (s *serverMap) Get(id string) (*server, bool) {
	s.RLock()
	defer s.RUnlock()
	val, ok := s.servers[id]
	return val, ok
}

type server struct {
	sync.RWMutex
	raidMode    bool
	users       map[string]*rate.Limiter
	joinedCache []*cacheUser
	lastRaid    []string
}

func (s *server) Add(id string) {
	s.Lock()
	defer s.Unlock()
	s.users[id] = rate.NewLimiter(1, 2)
}
func (s *server) Remove(id string) {
	s.Lock()
	defer s.Unlock()
	delete(s.users, id)
}
func (s *server) GetUser(id string) (*rate.Limiter, bool) {
	s.RLock()
	defer s.RUnlock()
	val, ok := s.users[id]
	return val, ok
}
func (s *server) RaidMode() bool {
	return s.raidMode
}
func (s *server) RaidToggle() {
	s.raidMode = !s.raidMode
}
func (s *server) AddToJoinCache(id string) {
	s.Lock()
	defer s.Unlock()
	s.joinedCache = append(s.joinedCache, &cacheUser{
		u: id,
		e: time.Now().Add(time.Hour).UnixNano(),
	})
}

/*
func (s *server) GetLastRaid() []string {
	return s.lastRaid
}
*/

func (s *serverMap) removeOld() {
	for _, v := range s.servers {
		for i, v2 := range v.joinedCache {
			if v2.Expired() {
				v.joinedCache[i] = v.joinedCache[len(v.joinedCache)-1]
				v.joinedCache[len(v.joinedCache)-1] = nil
				v.joinedCache = v.joinedCache[:len(v.joinedCache)-1]
			}
		}
	}
}

func (s *serverMap) runCleaner() {
	t := time.NewTicker(time.Hour)
	for {
		select {
		case <-t.C:
			s.removeOld()
		}
	}
}

type cacheUser struct {
	u string
	e int64
}

func (c *cacheUser) Expired() bool {
	return time.Now().UnixNano() > c.e
}
