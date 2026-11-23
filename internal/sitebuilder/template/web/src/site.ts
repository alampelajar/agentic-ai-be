// Written by the builder from the brief. Pages read the site's facts from here.
export type SitePage = { path: string; label: string }

export const site = {
  name: 'Website',
  tagline: '',
  language: 'en' as 'id' | 'en',
  pages: [{ path: '/', label: 'Home' }] as SitePage[],
  contact: { phone: '', whatsapp: '', email: '', address: '', hours: '' },
  social: {} as Record<string, string>,
  images: [] as { src: string; alt: string }[],
  logo: '',
  products: [] as { name: string; image: string; alt: string }[],
}

/** The illustration of a product named in the brief, or undefined when it has none. */
export function productImage(name: string): { src: string; alt: string } | undefined {
  const wanted = name.trim().toLowerCase()
  const product = site.products.find((item) => item.image && item.name.trim().toLowerCase() === wanted)
  return product ? { src: product.image, alt: product.alt } : undefined
}

/** A WhatsApp link with a prepared message, or '' when the site has no WhatsApp number. */
export function whatsappLink(message = ''): string {
  const number = site.contact.whatsapp.replace(/[^0-9]/g, '')
  if (!number) return ''
  return `https://wa.me/${number}${message ? `?text=${encodeURIComponent(message)}` : ''}`
}
