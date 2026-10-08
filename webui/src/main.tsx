import * as Tooltip from '@radix-ui/react-tooltip'
import { QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Toaster } from 'sonner'
import { Route, Switch } from 'wouter'
import { Palette } from './components/Palette'
import { Help, Shell } from './components/Shell'
import { Empty } from './components/ui'
import { queryClient } from './lib/api'
import Changes from './pages/Changes'
import Documents from './pages/Documents'
import EntityPage from './pages/Entity'
import Hosts from './pages/Hosts'
import Kubernetes from './pages/Kubernetes'
import Logs from './pages/Logs'
import Problem from './pages/Problem'
import Problems from './pages/Problems'
import Pulse from './pages/Pulse'
import Query from './pages/Query'
import Ai from './pages/Ai'
import AiConversation from './pages/AiConversation'
import Experience, { Session } from './pages/Rum'
import Security from './pages/Security'
import Services from './pages/Services'
import Smartscape from './pages/Smartscape'
import Traces, { Trace } from './pages/Traces'
import './styles.css'

function App() {
  return (
    <Shell>
      <Switch>
        <Route path="/" component={Pulse} />
        <Route path="/problems" component={Problems} />
        <Route path="/problems/:id">{(p) => <Problem key={p.id} id={decodeURIComponent(p.id)} />}</Route>
        <Route path="/services" component={Services} />
        <Route path="/k8s" component={Kubernetes} />
        <Route path="/hosts" component={Hosts} />
        <Route path="/logs" component={Logs} />
        <Route path="/traces" component={Traces} />
        <Route path="/traces/:id">{(p) => <Trace key={p.id} id={decodeURIComponent(p.id)} />}</Route>
        <Route path="/rum" component={Experience} />
        <Route path="/rum/sessions/:id">{(p) => <Session key={p.id} id={decodeURIComponent(p.id)} />}</Route>
        <Route path="/ai" component={Ai} />
        <Route path="/ai/conversations/:id">{(p) => <AiConversation key={p.id} id={decodeURIComponent(p.id)} />}</Route>
        <Route path="/changes" component={Changes} />
        <Route path="/security" component={Security} />
        <Route path="/query" component={Query} />
        <Route path="/smartscape" component={Smartscape} />
        <Route path="/e/:id">{(p) => <EntityPage key={p.id} id={decodeURIComponent(p.id)} />}</Route>
        <Route path="/docs" component={Documents} />
        <Route>
          <Empty title="Page not found" hint="Press ⌘K to jump anywhere." className="h-full" />
        </Route>
      </Switch>
    </Shell>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <Tooltip.Provider>
        <App />
        <Palette />
        <Help />
        <Toaster theme="system" position="bottom-right" toastOptions={{ className: '!bg-raised !text-ink !border-line-strong' }} />
      </Tooltip.Provider>
    </QueryClientProvider>
  </StrictMode>,
)
