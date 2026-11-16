import type { ReactNode } from 'react'
import { Link } from '../router'
import { site } from '../site'

// The starting layout. The agent replaces it with one that follows the design direction.
export default function Layout({ children }: { children: ReactNode }) {
  return (
    <div className='min-h-screen bg-background font-body text-foreground'>
      <header className='border-b border-border'>
        <nav className='mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-4 px-4 py-4'>
          <Link to='/' className='font-heading text-xl'>
            {site.name}
          </Link>
          <div className='flex flex-wrap gap-4 text-sm'>
            {site.pages.map((page) => (
              <Link key={page.path} to={page.path}>
                {page.label}
              </Link>
            ))}
          </div>
        </nav>
      </header>
      <main>{children}</main>
      <footer className='border-t border-border px-4 py-8 text-center text-sm text-muted'>{site.name}</footer>
    </div>
  )
}
