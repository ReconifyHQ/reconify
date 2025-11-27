# Self-Hosting Guide

This guide explains how to self-host Reconify for production use.

## Quick Start (Development)

The easiest way to get started is using Docker Compose:

```bash
# Build and start all services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down
```

Once running, access:
- **API**: http://localhost:3000
- **Dashboard**: http://localhost:8080

## Production Setup

For production deployment, you'll need to:

1. **Set up a reverse proxy** (nginx, Caddy, Traefik)
2. **Configure SSL certificates** (Let's Encrypt recommended)
3. **Configure domain/DNS**

### Using Docker Compose

1. Clone the repository:
   ```bash
   git clone https://github.com/reconify/reconify.git
   cd reconify
   ```

2. Build and start services:
   ```bash
   docker-compose up -d
   ```

3. Configure your reverse proxy (see nginx example below)

### Manual Installation

1. **Build CLI**:
   ```bash
   make build:cli
   # Binary will be in cli/reconify
   ```

2. **Install Node.js dependencies**:
   ```bash
   pnpm install
   ```

3. **Build API and Dashboard**:
   ```bash
   pnpm build
   ```

4. **Start services**:
   ```bash
   # API
   pnpm --filter api start

   # Dashboard (in another terminal)
   pnpm --filter dashboard start
   ```

## Reverse Proxy Configuration

### Nginx Example

Create `/etc/nginx/sites-available/reconify`:

```nginx
server {
    listen 80;
    server_name reconify.example.com;

    # Redirect HTTP to HTTPS
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name reconify.example.com;

    # SSL configuration (Let's Encrypt)
    ssl_certificate /etc/letsencrypt/live/reconify.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/reconify.example.com/privkey.pem;

    # API proxy
    location /api {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Dashboard
    location / {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }
}
```

Enable the site:
```bash
sudo ln -s /etc/nginx/sites-available/reconify /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### SSL with Let's Encrypt

```bash
# Install certbot
sudo apt-get install certbot python3-certbot-nginx

# Obtain certificate
sudo certbot --nginx -d reconify.example.com

# Auto-renewal (already configured by certbot)
```

### Caddy Example

Create `Caddyfile`:

```
reconify.example.com {
    reverse_proxy /api localhost:3000
    reverse_proxy / localhost:8080
}
```

Caddy automatically handles SSL certificates.

## Environment Variables

### API

- `NODE_ENV`: Set to `production` for production
- `PORT`: API port (default: 3000)

### Dashboard

- `API_URL`: Backend API URL (default: http://localhost:3000)

## Security Considerations

1. **Firewall**: Only expose ports 80/443, not 3000/8080 directly
2. **SSL**: Always use HTTPS in production
3. **Authentication**: Add authentication layer (not included in PoC)
4. **Rate Limiting**: Configure rate limiting in reverse proxy
5. **File Uploads**: Limit file size in nginx/API

## Troubleshooting

### Services won't start

```bash
# Check logs
docker-compose logs

# Check if ports are in use
netstat -tulpn | grep -E '3000|8080'
```

### API can't find CLI binary

Ensure the CLI binary is built and available:
```bash
make build:cli
```

### Permission issues

```bash
# Make CLI executable
chmod +x cli/reconify
```

## Support

For issues and questions, please open an issue on GitHub.

