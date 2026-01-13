package main

type Server struct {
	ID       string
	Name     string
	IconURL  string
	Channels []*Channel
}

type Channel struct {
	ID       string
	Name     string
	ServerID string
	Messages []*Message
}

type Message struct {
	ID        string
	Author    string
	Content   string
	AvatarURL string
}
