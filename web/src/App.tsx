import { useEffect } from 'react'
import { Link, Route, Routes, useLocation } from 'react-router-dom'
import { Nav } from './components/Nav'
import { Footer } from './components/Footer'
import { Landing } from './pages/Landing'
import { DocsLayout } from './pages/docs/DocsLayout'
import { DocsIndex } from './pages/docs/DocsIndex'
import { GettingStarted } from './pages/docs/GettingStarted'
import { Concepts } from './pages/docs/Concepts'
import { CliReference } from './pages/docs/CliReference'
import { Configuration } from './pages/docs/Configuration'
import { Operations } from './pages/docs/Operations'

function ScrollManager() {
  const location = useLocation()

  useEffect(() => {
    const { hash } = location
    if (hash) {
      const id = hash.replace('#', '')
      const t = window.setTimeout(() => {
        const el = document.getElementById(id)
        if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }, 50)
      return () => window.clearTimeout(t)
    }
    window.scrollTo({ top: 0, behavior: 'instant' })
  }, [location.pathname, location.hash])

  return null
}

export default function App() {
  return (
    <div className="flex min-h-svh flex-col">
      <ScrollManager />
      <Nav />
      <main className="flex-1">
        <Routes>
          <Route path="/" element={<Landing />} />
          <Route path="/docs" element={<DocsLayout />}>
            <Route index element={<DocsIndex />} />
            <Route path="getting-started" element={<GettingStarted />} />
            <Route path="concepts" element={<Concepts />} />
            <Route path="cli" element={<CliReference />} />
            <Route path="configuration" element={<Configuration />} />
            <Route path="operations" element={<Operations />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
      <Footer />
    </div>
  )
}

function NotFound() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-32 text-center sm:px-6 lg:px-8">
      <p className="font-mono text-6xl font-semibold tracking-tight">404</p>
      <h1 className="mt-4 text-2xl font-semibold tracking-tight">
        Page not found
      </h1>
      <p className="mt-3 text-neutral-600 dark:text-neutral-400">
        The page you&rsquo;re looking for doesn&rsquo;t exist.
      </p>
      <Link
        to="/"
        className="pressable mt-6 inline-flex h-11 items-center rounded-full bg-black px-6 text-sm font-medium text-white hover:opacity-90 dark:bg-white dark:text-black"
      >
        Back to home
      </Link>
    </div>
  )
}
