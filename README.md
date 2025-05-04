# RUNE Discord Selfbot

A powerful Discord selfbot written in Go with various utility and entertainment features.

⚠️ **Disclaimer**: Using selfbots is against Discord's Terms of Service. Use at your own risk.

## Features

### Utility Commands
- `ping` - Check bot latency
- `clear [count]` - Delete messages (default: 10)
- `say <message>` - Make the bot say something
- `weather [location]` - Get current weather information
- `ar` - Toggle auto responder
- `ap @user` - Start autopressure on user
- `status` - Change Discord status
- `ip <address>` - Lookup IP information
- `encode/decode` - Base64 encoding/decoding
- `password [length]` - Generate secure passwords
- `ai [prompt]` - Get AI-generated responses
- `shorten <url>` - URL shortener
- `setprefix [prefix]` - Change command prefix
- `nitrosniper` - Automatic nitro code claiming

### Fun Commands
- `8ball <question>` - Ask the magic 8-ball
- `roll [sides]` - Roll a die
- `rizz` - Get random pickup lines
- `femboy` - Calculate femboy percentage
- `quote` - Random quotes
- `joke` - Random jokes
- `urban <term>` - Urban Dictionary lookup
- `coinflip` - Flip a coin
- `fact` - Random facts
- `meme` - Random meme phrases

### Info Commands
- `whoami` - Show user info
- `avatar` - Get avatar URL
- `stats` - Show bot statistics
- `credits` - Display bot credits

### NSFW Commands
- `psearch <term>` - PornHub search
- `tits` - Random NSFW images
- `catgirl` - Random catgirl images

## Setup

1. Clone the repository
2. Create a `config.json` file with the following structure:
```json
{
    "token": "YOUR_DISCORD_TOKEN",
    "OwnerID": "YOUR_DISCORD_USER_ID",
    "prefix": "&",
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
- `gemini_api_key`: API key for AI responses
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

- snow - inspiration
- https://github.com/skifli/gocord - api wrapper
- gpt4.1 - debugging 😭

## Author

Created by Eclipse
