import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import './index.css'
import App from './App.tsx'
import { captureTokenFromLocation } from '@/lib/auth/token'

// Before the router mounts: read #token=, keep it in memory, strip the fragment.
captureTokenFromLocation()

// A data router, so views can block navigation (useBlocker). App keeps using <Routes>.
const router = createBrowserRouter([{ path: '*', element: <App /> }])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
)
