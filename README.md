# Rune - A Fun Discord Selfbot

Hey there! Rune is a lightweight and feature-packed selfbot for Discord, built in Go. It's designed to add some useful tools, silly fun, and even a bit of edge to your Discord experience—all running from your own account.

**Important Warning**: Selfbots like this go against Discord's Terms of Service. If you use it, there's a real risk of your account getting banned. I'm not responsible for anything that happens—use it at your own risk, and maybe on a throwaway account if you're just testing.

## What Can It Do?

Rune comes with a bunch of commands, grouped into categories. The default prefix is `&`, but you can change it.

### Utility Commands
These help with everyday things:
- `&ping` — Check how fast it's responding
- `&clear [count]` — Delete your recent messages (defaults to 10)
- `&weather [location] (api became payed)` — Get the current weather for a place
- `&ar` — Toggle an auto-responder on/off
- `&ap @user` — Start "autopressure" on a mentioned user (spam pings with message)
- `&status` — Set a custom Discord status
- `&ip <address>` — Look up info about an IP
- `&encode` / `&decode` — Base64 encoding and decoding
- `&password [length]` — Generate a strong random password
- `&ai <prompt> (removed)` — Chat with Google's Gemini AI for smart (or fun) responses
- `&shorten <url>` — Shorten a long URL
- `&setprefix <new>` — Change the command prefix
- `&nitrosniper (notworking)` — Automatically claim a nitro gift when sent in channels you can access
- `&google <query>` — Googles something

### Fun Commands (For Laughs)
- `&8ball <question>` — Ask the magic 8-ball for advice
- `&roll [sides]` — Roll a dice (default 6 sides)
- `&rizz` — Get a random pickup line
- `&femboy` — Calculates your "femboy percentage" (purely for memes)
- `&quote` — A random inspirational (or silly) quote
- `&joke` — Hear a random joke
- `&urban <term>` — Look up slang on Urban Dictionary
- `&coinflip` — Heads or tails?
- `&fact` — A random interesting fact
- `&meme` — Some random meme text

### Info Commands (About You or the Bot)
- `&whoami` — Shows your user info
- `&avatar` — Gets your avatar URL
- `&stats` — Bot uptime and stats
- `&credits` — Shoutouts to helpers

### NSFW Commands (18+ Only, Use Responsibly)
These pull adult content—keep it private!
- `&psearch <term>` — Search on PornHub
- `&tits` — Random NSFW images
- `&catgirl` — Random catgirl images

In Discord, try `&help` for the full list, `&categories` for an overview, or things like `&utilities` to list just one group.

## How to Get It Running

It's super simple if you have Go installed:

1. Clone the repository

2. Create a `config.json` file with the following structure or use config.json.example and rename it to config.json:
```json
{
    "token": "YOUR_DISCORD_TOKEN",
    "OwnerID": "YOUR_DISCORD_USER_ID",
    "prefix": "PREFIX",
    "gemini_api_key": "YOUR_GEMINI_API_KEY",
    "auto_response_enabled": false,
    "auto_response_phrase": ""
}
```
3. Install dependencies:
```bash
go mod tidy
```
4. Build and run:
```bash
go build
./selfbot or .\selfbot.exe
```

## Configuration

- `token`: Your Discord user token
- `OwnerID`: Your Discord user ID
- `prefix`: Command prefix (default: &)
- `gemini_api_key`: API key for AI responses (i think it needs to be payed so i removed the feature)
- `auto_response_enabled`: Enable/disable auto responses
- `auto_response_phrase`: Custom auto response message

## Dependencies

- github.com/gorilla/websocket - WebSocket client
- google.golang.org/genai - Gemini AI integration

## Commands

Use `&help` to see all available commands
Use `&categories` to view command categories
Use `&utilities`, `&fun`, `&info`, or `&nsfw` to see specific command categories

## Credits

- advrso - inspiration
- https://github.com/skifli/gocord - api wrapper
- gpt4.1 - debugging

## Author

Created by Eclipse

