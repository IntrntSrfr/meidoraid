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
	"github.com/intrntsrfr/owo"
	"golang.org/x/time/rate"
)

type Config struct {
	Token  string `json:"token"`
	Owner  string `json:"owner"`
	OwoKey string `json:"owo_key"`
}

var (
	servers = &serverMap{Servers: make(map[string]*server)}
	oc      *owo.Client
	config  Config
)

func (s *serverMap) save() {
	d, _ := json.Marshal(s)
	ioutil.WriteFile("./test.json", d, 0644)
}

func (s *serverMap) load() {
	d, _ := ioutil.ReadFile("./test.json")
	json.Unmarshal(d, s)
}

func main() {
	f, err := ioutil.ReadFile("./config.json")
	if err != nil {
		fmt.Println(err)
		return
	}

	var config Config
	json.Unmarshal(f, &config)

	servers.load()
	defer servers.save()

	client, err := discordgo.New("Bot " + config.Token)
	if err != nil {
		fmt.Println(err)
		return
	}
	oc = owo.NewClient(config.OwoKey)

	go servers.runCleaner()

	addHandlers(client)

	err = client.Open()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()

	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc


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
		fmt.Println("bad user", m.GuildID, m.User.ID)

		srv.lastRaid[m.User.ID] = struct{}{}
		//s.GuildBanCreateWithReason(m.GuildID, m.User.ID, "Raid measure", 7)
	}
}

func RaidToggleHandler(s *discordgo.Session, m *discordgo.MessageCreate) {

	if m.Author.Bot || len(m.Content) <= 0 {
		return
	}

	srv, ok := servers.Get(m.GuildID)
	if !ok {
		return
	}

	perms, err := s.State.UserChannelPermissions(m.Author.ID, m.ChannelID)
	if err != nil {
		return
	}
	botPerms, err := s.State.UserChannelPermissions(s.State.User.ID, m.ChannelID)
	if err != nil {
		return
	}

	if perms&discordgo.PermissionBanMembers == 0 && perms&discordgo.PermissionAdministrator == 0 {
		return
	}

	if botPerms&discordgo.PermissionBanMembers == 0 && perms&discordgo.PermissionAdministrator == 0 {
		return
	}

	args := strings.Fields(m.Content)

	switch strings.ToLower(args[0]) {
	case "m?raidmode":
		srv.RaidToggle(s)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("raid mode set to %v", srv.RaidMode()))
	case "m?lastraid":
		l := srv.GetLastRaid()
		if len(l) <= 0 {
			s.ChannelMessageSend(m.ChannelID, "no last raid")
			return
		}
		res, err := oc.Upload(strings.Join(l, " "))
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "Error getting last raid. try again?")
			return
		}
		s.ChannelMessageSend(m.ChannelID, res)
	case "m?raidignore":
		if len(args) < 2 {
			return
		}
		srv.IgnoreRole = args[1]
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("raid will ignore users with role id: %v", args[1]))
	default:
		return
	}
}

func MessageCreateHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	srv, ok := servers.Get(m.GuildID)
	if !ok {
		return
	}

	if !srv.RaidMode() {
		return
	}

	if hasRole(m.Member, srv.IgnoreRole) {
		return
	}

	usr, ok := srv.GetUser(m.Author.ID)
	if !ok {
		srv.Add(m.Author.ID)
		return
	}

	if !usr.Allow() || len(m.Mentions) > 10 {
		// ban the user
		fmt.Println("bad user", m.GuildID, m.Author.ID)
		srv.lastRaid[m.Author.ID] = struct{}{}
		//s.GuildBanCreateWithReason(m.GuildID, m.User.ID, "Raid measure", 7)
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

func hasRole(m *discordgo.Member, role string) bool {
	if role == "" {
		return false
	}
	for _, r := range m.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type serverMap struct {
	sync.RWMutex
	Servers map[string]*server
}

func (s *serverMap) Add(id string) {
	s.Lock()
	defer s.Unlock()
	srv, found := s.Servers[id]
	if !found {
		s.Servers[id] = &server{
			id:          id,
			raidMode:    false,
			users:       make(map[string]*rate.Limiter),
			joinedCache: make(map[string]*cacheUser),
			lastRaid:    make(map[string]struct{}),
		}
	} else {
		s.Servers[id] = &server{
			id:          id,
			raidMode:    false,
			users:       make(map[string]*rate.Limiter),
			joinedCache: make(map[string]*cacheUser),
			lastRaid:    make(map[string]struct{}),
			IgnoreRole:  srv.IgnoreRole,
		}
	}
	fmt.Println(fmt.Sprintf("added server id: %v", id))
}
func (s *serverMap) Remove(id string) {
	s.Lock()
	defer s.Unlock()
	delete(s.Servers, id)
}
func (s *serverMap) Get(id string) (*server, bool) {
	s.RLock()
	defer s.RUnlock()
	val, ok := s.Servers[id]
	return val, ok
}

type server struct {
	sync.RWMutex
	id          string
	raidMode    bool
	users       map[string]*rate.Limiter
	joinedCache map[string]*cacheUser
	lastRaid    map[string]struct{}
	IgnoreRole  string
}

func (s *server) Add(id string) {
	s.Lock()
	defer s.Unlock()
	s.users[id] = rate.NewLimiter(rate.Every(time.Second*2), 2)
	fmt.Println(fmt.Sprintf("%v: added user limiter: %v", s.id, id))
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
func (s *server) RaidToggle(sess *discordgo.Session) {
	if s.raidMode {
		// raid mode is being turned off
		s.users = make(map[string]*rate.Limiter)

	} else {
		// raid mode is being turned on

		// new lastraid map
		s.lastRaid = make(map[string]struct{})

		// look through join cache for new bad users and ban said users, and add them to the raidmap
		for _, u := range s.joinedCache {
			if isNewAccount(u.u) {
				s.lastRaid[u.u] = struct{}{}

				// ban the user
				//sess.GuildBanCreateWithReason(s.ID, u.u, "Raid measure", 7)
			}
		}

	}
	s.raidMode = !s.raidMode
}
func (s *server) AddToJoinCache(id string) {
	s.Lock()
	defer s.Unlock()
	s.joinedCache[id] = &cacheUser{
		u: id,
		e: time.Now().Add(time.Hour).UnixNano(),
	}
	fmt.Println(fmt.Sprintf("%v: added user to join cache: %v", s.id, id))
}

func (s *server) GetLastRaid() []string {
	var l []string
	for k := range s.lastRaid {
		l = append(l, k)
	}
	return l
}

func (s *serverMap) removeOld() {
	for i, v := range s.Servers {
		s.Lock()
		for j, v2 := range v.joinedCache {
			if v2.Expired() {
				fmt.Println(fmt.Sprintf("%v: user expired: %v", i, v2.u))
				delete(v.joinedCache, j)
			}
		}
		s.Unlock()
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
