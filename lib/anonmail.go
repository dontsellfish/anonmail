package lib

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	tele "gopkg.in/telebot.v3"
	"strconv"
	"strings"
	"time"
)

const DefaultConfigPath = "./cfg.json"
const DefaultRedisDatabaseAddress = "localhost:6379"
const DefaultStartMessage = "Hello, send me anything and I'll forward it to my owner"
const DefaultRedisBannedListLayoutKey = "anonmail_%d_banned_list"

/*
REDIS DB STRUCTURE
{
	bot.bannedUsersKey: RedisSets{<user-id>...},
	<message-id-in-forwarded-chat>: <original-sender-id> {SendAsReply, SendWithoutReply} [reply-to-id],...
}
*/

const (
	SendWithoutReply = iota
	SendAsReply
)

type Config struct {
	Token                string  `json:"token"`
	AdminList            []int64 `json:"admin-list"`
	ForwardChatID        int64   `json:"forward-chat-id"`
	RedisDatabaseAddress string  `json:"redis-database-address,omitempty"`
	RedisDatabaseId      int     `json:"redis-database-id,omitempty"`
	StartMessage         string  `json:"start-message,omitempty"`
}

type Bot struct {
	Bot *tele.Bot
	Cfg Config

	bannedUsersKey string
	redisDB        *redis.Client
	redisContext   context.Context
}

func NewBot(cfg Config, settings ...tele.Settings) (*Bot, error) {
	var pref tele.Settings
	if len(settings) != 0 {
		pref = settings[0]
	} else {
		pref = tele.Settings{
			Poller:      &tele.LongPoller{Timeout: time.Second * 30},
			Token:       cfg.Token,
			Synchronous: true,
		}
	}

	if cfg.RedisDatabaseAddress == "" {
		cfg.RedisDatabaseAddress = DefaultRedisDatabaseAddress
	}
	if cfg.StartMessage == "" {
		cfg.StartMessage = DefaultStartMessage
	}

	bot, err := tele.NewBot(pref)
	if err != nil {
		return nil, err
	}

	bot.OnError = func(err error, ctx tele.Context) {
		var chatName string
		if ctx.Chat().Username != "" {
			chatName = fmt.Sprintf("@%s", ctx.Chat().Username)
		} else if ctx.Chat().Title != "" {
			chatName = ctx.Chat().Title
		} else {
			chatName = fmt.Sprintf("%s %s", ctx.Chat().FirstName, ctx.Chat().LastName)
		}
		for _, admin := range cfg.AdminList {
			_, _ = bot.Send(&tele.Chat{ID: admin},
				fmt.Sprintf("Error :c\n\n	%s\n\nAt: '%s' [%d]", err.Error(), chatName, ctx.Chat().ID))
		}
	}

	rights, err := bot.ChatMemberOf(&tele.Chat{ID: cfg.ForwardChatID}, bot.Me)
	if err != nil {
		return nil, err
	}
	if rights.Role != "administrator" {
		if !rights.CanSendMessages {
			return nil, errors.New("CanSendMessages is false")
		}
		if !rights.CanSendMedia {
			return nil, errors.New("CanPostMessages is false")
		}
		if !rights.User.CanReadMessages {
			return nil, errors.New("CanReadMessages is false")
		}
		if !rights.User.SupportsInline {
			return nil, errors.New("SupportsInline is false")
		}
	}

	var rdb = redis.NewClient(
		&redis.Options{
			Addr: cfg.RedisDatabaseAddress,
			DB:   cfg.RedisDatabaseId,
		})

	bannedUsersKey := fmt.Sprintf(DefaultRedisBannedListLayoutKey, cfg.ForwardChatID)
	redisContext := context.Background()
	err = rdb.SAdd(redisContext, bannedUsersKey, "777").Err()
	if err != nil {
		return nil, err
	}

	contains := false
	for _, adminId := range cfg.AdminList {
		if adminId == cfg.ForwardChatID {
			contains = true
			break
		}
	}
	if !contains {
		cfg.AdminList = append(cfg.AdminList, cfg.ForwardChatID)
	}

	return &Bot{Bot: bot, Cfg: cfg, bannedUsersKey: bannedUsersKey, redisDB: rdb, redisContext: redisContext}, nil
}

func (bot *Bot) AlertAdmins(comments ...string) {
	msgText := strings.Join(comments, "\n\n")
	for _, admin := range bot.Cfg.AdminList {
		_, _ = bot.Bot.Send(&tele.Chat{ID: admin}, msgText)
	}
}

func (bot *Bot) Start() {
	err := bot.Bot.SetCommands([]tele.Command{
		{"/ok", "check if bot is alive"},
	})
	if err != nil {
		bot.AlertAdmins(fmt.Sprintf(err.Error(), "while settings commands"))
	}

	universalHandler := bot.makeUniversalHandler()

	bot.Bot.Handle(tele.OnText, universalHandler)
	bot.Bot.Handle(tele.OnMedia, universalHandler)

	bot.Bot.Handle("/start", func(ctx tele.Context) error {
		return ctx.Send(bot.Cfg.StartMessage, &tele.SendOptions{ParseMode: tele.ModeMarkdownV2})
	})

	bot.Bot.Handle("/ban", func(ctx tele.Context) error {
		if bot.isFromForwardChat(ctx) && isReply(ctx) {
			chatId, _, err := bot.GetOriginalChat(ctx)
			if err != nil {
				return err
			}
			err = bot.Ban(chatId)
			if err != nil {
				return err
			}
			return ctx.Reply("banned")
		}

		return universalHandler(ctx)
	})

	bot.Bot.Handle("/unban", func(ctx tele.Context) error {
		if bot.isFromForwardChat(ctx) && isReply(ctx) {
			chatId, _, err := bot.GetOriginalChat(ctx)
			if err != nil {
				return err
			}
			err = bot.Unban(chatId)
			if err != nil {
				return err
			}
			return ctx.Reply("unbanned")
		}

		return universalHandler(ctx)
	})

	bot.Bot.Start()
}

func (bot *Bot) Stop() {
	bot.Bot.Stop()
}

func (bot *Bot) GetOriginalChat(ctx tele.Context) (int64, *tele.SendOptions, error) {
	info, err := bot.redisDB.Get(bot.redisContext, toKey(bot, ctx.Message().ReplyTo.ID)).Result()
	if err != nil {
		return 0, nil, err
	}
	if !strings.Contains(info, " ") {
		return 0, nil, errors.New("wrong format")
	}

	args := strings.Split(info, " ")
	chatId, _ := strconv.ParseInt(args[0], 10, 64)
	howToSend, _ := strconv.Atoi(args[1])

	sendOptions := tele.SendOptions{ReplyTo: nil}
	if howToSend == SendAsReply {
		originalMessageId, _ := strconv.Atoi(args[2])
		sendOptions.ReplyTo = &tele.Message{ID: originalMessageId, Chat: &tele.Chat{ID: chatId}}
	}

	return chatId, &sendOptions, nil
}

func (bot *Bot) Ban(userId int64) error {
	return bot.redisDB.SAdd(bot.redisContext, bot.bannedUsersKey, toKey(bot, userId)).Err()
}

func (bot *Bot) Unban(userId int64) error {
	return bot.redisDB.SRem(bot.redisContext, bot.bannedUsersKey, toKey(bot, userId)).Err()
}

func (bot *Bot) makeUniversalHandler() func(ctx tele.Context) error {
	forwarderToForwardChat := bot.makeForwarderToForwardChat()
	forwarderToOriginalSender := bot.makeForwarderToOriginalSender()

	return func(ctx tele.Context) error {
		if isPersonalMessage(ctx) {
			return forwarderToForwardChat(ctx)
		} else if bot.isFromForwardChat(ctx) && isReply(ctx) {
			return forwarderToOriginalSender(ctx)
		}
		return nil
	}
}

func (bot *Bot) makeForwarderToOriginalSender() func(tele.Context) error {
	return func(ctx tele.Context) error {
		info, err := bot.redisDB.Get(bot.redisContext, toKey(bot, ctx.Message().ReplyTo.ID)).Result()
		if err != nil {
			return err
		}
		if !strings.Contains(info, " ") {
			return errors.New("wrong format")
		}

		args := strings.Split(info, " ")
		chatId, _ := strconv.ParseInt(args[0], 10, 64)
		howToSend, _ := strconv.Atoi(args[1])

		sendOptions := tele.SendOptions{ReplyTo: nil}
		if howToSend == SendAsReply {
			originalMessageId, _ := strconv.Atoi(args[2])
			sendOptions.ReplyTo = &tele.Message{ID: originalMessageId, Chat: &tele.Chat{ID: chatId}}
		}

		_, err = bot.Bot.Copy(&tele.Chat{ID: chatId}, ctx.Message(), &sendOptions)

		return err
	}
}

func (bot *Bot) makeForwarderToForwardChat() func(tele.Context) error {
	return func(ctx tele.Context) error {
		isMember, err := bot.redisDB.SIsMember(bot.redisContext, bot.bannedUsersKey, toKey(bot, ctx.Chat().ID)).Result()
		if err != nil {
			return err
		}
		if isMember {
			return ctx.Send("you are banned :S")
		}

		msg, err := bot.Bot.Send(&tele.Chat{ID: bot.Cfg.ForwardChatID},
			toUserInfoMarkdown(ctx),
			&tele.SendOptions{ParseMode: tele.ModeMarkdownV2},
		)
		if err != nil {
			return err
		}
		err = bot.redisDB.Set(bot.redisContext, toKey(bot, msg.ID), fmt.Sprintf("%d %d", ctx.Sender().ID, SendWithoutReply), 0).Err()

		msg, err = bot.Bot.Forward(&tele.Chat{ID: bot.Cfg.ForwardChatID}, ctx.Message())
		if err != nil {
			return err
		}
		err = bot.redisDB.Set(bot.redisContext, toKey(bot, msg.ID), fmt.Sprintf("%d %d %d", ctx.Sender().ID, SendAsReply, ctx.Message().ID), 0).Err()

		return err
	}
}

func (bot *Bot) isFromForwardChat(ctx tele.Context) bool {
	return ctx.Chat().ID == bot.Cfg.ForwardChatID
}

func toKey[K any](bot *Bot, id K) string {
	return fmt.Sprintf("anonmail_%d_%d", bot.Cfg.ForwardChatID, id)
}

func toUserInfoMarkdown(ctx tele.Context) string {
	userInfo := escapeTgMarkdownV2SpecialSymbols(fmt.Sprintf("%s %s", ctx.Sender().FirstName, ctx.Sender().LastName))
	if ctx.Sender().Username != "" {
		userInfo = fmt.Sprintf("[%s](https://t.me/%s)", userInfo, ctx.Sender().Username)
	}
	userInfo += fmt.Sprintf(" \\(%s\\)", fmt.Sprintf("[%s](tg://openmessage?user_id=%d)",
		escapeTgMarkdownV2SpecialSymbols(fmt.Sprintf("%d", ctx.Sender().ID)),
		ctx.Sender().ID,
	))
	return userInfo
}

func isPersonalMessage(ctx tele.Context) bool {
	return ctx.Chat().ID == ctx.Sender().ID
}

func isReply(ctx tele.Context) bool {
	return ctx.Message().ReplyTo != nil
}

func escapeTgMarkdownV2SpecialSymbols(text string) string {
	// escape chars: '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!'
	replacer := strings.NewReplacer("_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "~", "\\~", "`", "\\`", ">", "\\>", "#", "\\#", "+", "\\+", "-", "\\-", "=", "\\=", "|", "\\|", "{", "\\{", "}", "\\}", ".", "\\.", "!", "\\!")
	return replacer.Replace(text)
}
