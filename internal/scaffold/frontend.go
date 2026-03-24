package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

func writeFrontendSkeleton(outputDir, name string) error {
	fe := filepath.Join(outputDir, "frontend")

	files := map[string]string{

		filepath.Join(fe, "package.json"): fmt.Sprintf(`{
  "name": "%s-frontend",
  "private": true,
  "version": "0.0.1",
  "type": "module",
  "scripts": {
    "dev":     "vite",
    "build":   "tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "@radix-ui/react-slot":      "^1.1.0",
    "class-variance-authority":  "^0.7.0",
    "clsx":                      "^2.1.1",
    "lucide-react":              "^0.436.0",
    "react":                     "^18.3.1",
    "react-dom":                 "^18.3.1",
    "react-router-dom":          "^6.26.1",
    "tailwind-merge":            "^2.5.2"
  },
  "devDependencies": {
    "@types/react":        "^18.3.4",
    "@types/react-dom":    "^18.3.0",
    "@vitejs/plugin-react":"^4.3.1",
    "autoprefixer":        "^10.4.20",
    "postcss":             "^8.4.45",
    "tailwindcss":         "^3.4.10",
    "typescript":          "^5.5.3",
    "vite":                "^5.4.2"
  }
}
`, name),

		filepath.Join(fe, "vite.config.ts"): `import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
`,

		filepath.Join(fe, "tsconfig.json"): `{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "baseUrl": ".",
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["src"]
}
`,

		filepath.Join(fe, "postcss.config.js"): `export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
`,

		filepath.Join(fe, "tailwind.config.ts"): `import type { Config } from 'tailwindcss'

export default {
  darkMode: ['class'],
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        border:     'hsl(var(--border))',
        background: 'hsl(var(--background))',
        foreground: 'hsl(var(--foreground))',
        primary: {
          DEFAULT:    'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        muted: {
          DEFAULT:    'hsl(var(--muted))',
          foreground: 'hsl(var(--muted-foreground))',
        },
        card: {
          DEFAULT:    'hsl(var(--card))',
          foreground: 'hsl(var(--card-foreground))',
        },
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
    },
  },
  plugins: [],
} satisfies Config
`,

		filepath.Join(fe, "index.html"): fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>%s</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
`, name),

		filepath.Join(fe, "src", "main.tsx"): `import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
`,

		filepath.Join(fe, "src", "index.css"): `@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    --background:         0 0% 100%;
    --foreground:         224 71.4% 4.1%;
    --card:               0 0% 100%;
    --card-foreground:    224 71.4% 4.1%;
    --border:             220 13% 91%;
    --primary:            220.9 39.3% 11%;
    --primary-foreground: 210 20% 98%;
    --muted:              220 14.3% 95.9%;
    --muted-foreground:   220 8.9% 46.1%;
    --radius:             0.5rem;
  }
  .dark {
    --background:         224 71.4% 4.1%;
    --foreground:         210 20% 98%;
    --card:               224 71.4% 4.1%;
    --card-foreground:    210 20% 98%;
    --border:             215 27.9% 16.9%;
    --primary:            210 20% 98%;
    --primary-foreground: 220.9 39.3% 11%;
    --muted:              215 27.9% 16.9%;
    --muted-foreground:   217.9 10.6% 64.9%;
  }
}

* { border-color: hsl(var(--border)); }
body {
  background-color: hsl(var(--background));
  color: hsl(var(--foreground));
  font-feature-settings: "rlig" 1, "calt" 1;
}
`,

		filepath.Join(fe, "src", "App.tsx"): `import { Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider, useAuth } from '@/contexts/AuthContext'
import Layout from '@/components/Layout'
import Login from '@/pages/Login'
import Home from '@/pages/Home'

function PrivateRoute({ children }: { children: React.ReactNode }) {
  const { token } = useAuth()
  return token ? <>{children}</> : <Navigate to="/login" replace />
}

export default function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          path="/*"
          element={
            <PrivateRoute>
              <Layout />
            </PrivateRoute>
          }
        >
          <Route index element={<Home />} />
        </Route>
      </Routes>
    </AuthProvider>
  )
}
`,

		filepath.Join(fe, "src", "api", "client.ts"): `const BASE = import.meta.env.VITE_API_BASE ?? ''

export class ApiError extends Error {
  constructor(public code: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = localStorage.getItem('access_token')
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  }
  if (token) headers['Authorization'] = 'Bearer ' + token

  const res  = await fetch(BASE + path, { ...options, headers })
  const json = await res.json()

  if (json.code !== 0) {
    throw new ApiError(json.code, json.message ?? '请求失败')
  }
  return json.data as T
}

export const api = {
  get:    <T>(path: string)                => request<T>(path, { method: 'GET' }),
  post:   <T>(path: string, body: unknown) => request<T>(path, { method: 'POST',   body: JSON.stringify(body) }),
  put:    <T>(path: string, body: unknown) => request<T>(path, { method: 'PUT',    body: JSON.stringify(body) }),
  delete: <T>(path: string)               => request<T>(path, { method: 'DELETE' }),
}
`,

		filepath.Join(fe, "src", "contexts", "AuthContext.tsx"): `import { createContext, useContext, useState, useCallback } from 'react'
import { api } from '@/api/client'

interface AuthState {
  token:    string | null
  username: string | null
  userID:   number | null
}

interface AuthContextValue extends AuthState {
  login:  (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<AuthState>({
    token:    localStorage.getItem('access_token'),
    username: localStorage.getItem('username'),
    userID:   Number(localStorage.getItem('user_id')) || null,
  })

  const login = useCallback(async (username: string, password: string) => {
    const data = await api.post<{
      access_token:  string
      refresh_token: string
      username:      string
      user_id:       number
    }>('/api/v1/login', { username, password })

    localStorage.setItem('access_token',  data.access_token)
    localStorage.setItem('refresh_token', data.refresh_token)
    localStorage.setItem('username',      data.username)
    localStorage.setItem('user_id',       String(data.user_id))

    setState({ token: data.access_token, username: data.username, userID: data.user_id })
  }, [])

  const logout = useCallback(async () => {
    const rt = localStorage.getItem('refresh_token')
    if (rt) {
      await api.post('/api/v1/logout', { refresh_token: rt }).catch(() => {})
    }
    localStorage.clear()
    setState({ token: null, username: null, userID: null })
  }, [])

  return (
    <AuthContext.Provider value={{ ...state, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be inside AuthProvider')
  return ctx
}
`,

		filepath.Join(fe, "src", "components", "Layout.tsx"): fmt.Sprintf(`import { Outlet, NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '@/contexts/AuthContext'
import { LogOut } from 'lucide-react'

const nav: { to: string; label: string; icon: React.ElementType }[] = []

export default function Layout() {
  const { username, logout } = useAuth()
  const navigate = useNavigate()

  const handleLogout = async () => {
    await logout()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-background">
      <aside className="w-56 border-r flex flex-col py-6 px-3 gap-1">
        <div className="px-3 mb-6">
          <h1 className="text-lg font-semibold tracking-tight">%s</h1>
          <p className="text-xs text-muted-foreground mt-0.5">{username}</p>
        </div>
        {nav.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              [
                'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
                isActive
                  ? 'bg-primary text-primary-foreground font-medium'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              ].join(' ')
            }
          >
            <Icon size={16} />
            {label}
          </NavLink>
        ))}
        <div className="mt-auto">
          <button
            onClick={handleLogout}
            className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
          >
            <LogOut size={16} />
            退出登录
          </button>
        </div>
      </aside>
      <main className="flex-1 overflow-auto">
        <div className="mx-auto max-w-4xl px-8 py-8">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
`, name),

		filepath.Join(fe, "src", "pages", "Login.tsx"): fmt.Sprintf(`import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '@/contexts/AuthContext'
import { ApiError } from '@/api/client'

export default function Login() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error,    setError]    = useState('')
  const [loading,  setLoading]  = useState(false)
  const { login } = useAuth()
  const navigate  = useNavigate()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await login(username, password)
      navigate('/')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '登录失败，请重试')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40">
      <div className="w-full max-w-sm rounded-xl border bg-card p-8 shadow-sm">
        <h1 className="text-2xl font-semibold tracking-tight mb-1">%s</h1>
        <p className="text-sm text-muted-foreground mb-6">登录以继续</p>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium">用户名</label>
            <input
              type="text"
              value={username}
              onChange={e => setUsername(e.target.value)}
              required
              autoFocus
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium">密码</label>
            <input
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              required
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
            />
          </div>
          {error && <p className="text-sm text-red-500">{error}</p>}
          <button
            type="submit"
            disabled={loading}
            className="w-full rounded-md bg-primary py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
          >
            {loading ? '登录中…' : '登录'}
          </button>
        </form>
      </div>
    </div>
  )
}
`, name),

		filepath.Join(fe, "src", "pages", "Home.tsx"): fmt.Sprintf(`import { useAuth } from '@/contexts/AuthContext'

export default function Home() {
  const { username } = useAuth()
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-center">
      <h2 className="text-2xl font-semibold">欢迎使用 %s</h2>
      <p className="text-muted-foreground text-sm">你好，{username}。请从左侧导航开始。</p>
    </div>
  )
}
`, name),

		filepath.Join(fe, "Dockerfile"): `FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
`,

		filepath.Join(fe, "nginx.conf"): `server {
  listen 80;
  root /usr/share/nginx/html;
  index index.html;

  location / {
    try_files $uri $uri/ /index.html;
  }

  location /api/ {
    proxy_pass       http://backend:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
  }
}
`,

		filepath.Join(fe, ".gitignore"): `node_modules/
dist/
.env.local
.env.*.local
`,
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen frontend file %s: %w", path, err)
		}
	}
	return nil
}
