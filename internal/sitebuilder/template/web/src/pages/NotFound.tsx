import { Link } from '../router'
import { site } from '../site'

export default function NotFound() {
  const id = site.language === 'id'
  return (
    <section className='mx-auto max-w-2xl px-4 py-32 text-center'>
      <h1 className='font-heading text-4xl'>{id ? 'Halaman tidak ditemukan' : 'Page not found'}</h1>
      <p className='mt-4 text-muted'>{id ? 'Alamat ini tidak ada di situs kami.' : 'This address is not on our site.'}</p>
      <Link to='/' className='mt-8 inline-block rounded-md bg-primary px-5 py-2.5 text-primary-foreground'>
        {id ? 'Kembali ke beranda' : 'Back to the home page'}
      </Link>
    </section>
  )
}
