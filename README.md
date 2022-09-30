## Anonmail in Telegram

I wrote it, because the most popular _[solution](https://t.me/LivegramBot)_ for this task is so crappy, I've hated it.

Features **Anonmail** has, but **LivegramBot** lacks of:

- Forward user-forwarded messages (information who forwarded it is kept)
- See profile/write in personal messages to people who hide they account link when forwarding
- Ability to self-host and be sure, that no 3rd-parties see your mail
- Open-source


### Getting started

1. Install [Redis](https://redis.io/docs/getting-started/installation/) database
2. Build

```shell
git clone https://github.com/dontsellfish/anonmail
cd anonmail
go build
```

3. Create tg chat for forwarding, get its ID
4. Create tg bot, acquire its Token
5. Configure your anonmail, that's all (only first 3 fields are required)

```json
{
  "token": "5600000000:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "admin-list": [
    1700000000
  ],
  "forward-chat-id": -1001770000000,
  "start-message": "Hello, send me anything and I'll forward it to my owner",
  "redis-database-address": "localhost:6379",
  "redis-database-id": 13
}
```