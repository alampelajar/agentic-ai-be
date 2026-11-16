import { site } from '../site'

// The starting page. The agent replaces it.
export default function Home() {
  return (
    <section className='mx-auto max-w-6xl px-4 py-24'>
      <h1 className='font-heading text-4xl'>{site.name}</h1>
      <p className='mt-4 text-muted'>{site.tagline}</p>
    </section>
  )
}
