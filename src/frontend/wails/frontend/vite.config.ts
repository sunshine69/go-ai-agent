import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  // Base path for assets served by Wails
  // Wails will use its own asset server; Vite doesn't need to proxy here in dev,
  // but we'll set base to '' so Vite serves from the project root.
  base: '',
})
