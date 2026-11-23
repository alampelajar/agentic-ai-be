import type { ReactNode } from 'react'
import { Footer, Header } from '../blocks'

// The starting layout: the finished header and footer, shaped by the look. The agent may
// give them a short note and a closing line.
export default function Layout({ children }: { children: ReactNode }) {
  return (
    <div className='min-h-screen bg-background font-body text-foreground'>
      <Header />
      <main>{children}</main>
      <Footer />
    </div>
  )
}
