import { useEffect } from 'react'
import Layout from './components/Layout'
import { usePath } from './router'
import { routes } from './routes'
import { site } from './site'

export default function App() {
  const path = usePath()
  const route = routes.find((item) => item.path === path) ?? routes.find((item) => item.path === '*') ?? routes[0]
  const Page = route.component

  useEffect(() => {
    document.title = route.path === '/' ? site.name : `${route.title} | ${site.name}`
    document.documentElement.lang = site.language
  }, [route])

  return (
    <Layout>
      <Page />
    </Layout>
  )
}
