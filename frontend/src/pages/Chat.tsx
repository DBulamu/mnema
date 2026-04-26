import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, streamSSE } from '../lib/api';

type Conversation = {
  id: string;
  title?: string | null;
  created_at: string;
  updated_at: string;
};

type Message = {
  id: string;
  conversation_id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  created_at: string;
};

type ListConvs = { items: Conversation[] };
type GetConv = { conversation: Conversation; messages: Message[] };
type StartConv = { conversation: Conversation };

// SSE event payloads from POST /v1/conversations/{id}/messages/stream.
type ChatUserStored = { message: Message };
type ChatDelta = { text: string };
type ChatFinal = { user_message: Message; assistant_message: Message };
type ChatErrorEvent = { message: string };

export function ChatPage() {
  const qc = useQueryClient();
  const [activeId, setActiveId] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  // Live-streamed assistant draft for the active conversation. Cleared
  // when the final event arrives and the persisted assistant message
  // takes over from the messages query. Kept in component state so
  // React Query's cache stays a pure mirror of server-confirmed rows.
  const [streamingDraft, setStreamingDraft] = useState<string>('');
  const [streaming, setStreaming] = useState(false);
  const [streamError, setStreamError] = useState<string | null>(null);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  const conversations = useQuery({
    queryKey: ['conversations'],
    queryFn: () => api<ListConvs>('/v1/conversations?limit=50'),
  });

  // Auto-select the freshest thread on first load so the user lands
  // somewhere usable instead of an empty pane.
  useEffect(() => {
    if (activeId) return;
    const items = conversations.data?.items;
    if (items && items.length > 0) setActiveId(items[0].id);
  }, [conversations.data, activeId]);

  const conversation = useQuery({
    queryKey: ['conversation', activeId],
    queryFn: () => api<GetConv>(`/v1/conversations/${activeId}?limit=200`),
    enabled: !!activeId,
  });

  const startConv = useMutation({
    mutationFn: () => api<StartConv>('/v1/conversations', { method: 'POST' }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['conversations'] });
      setActiveId(res.conversation.id);
    },
  });

  // We stream the assistant reply over SSE so long replies feel
  // responsive. The user message is committed before the LLM call,
  // so we render an optimistic echo from `user_stored` (server-
  // generated id) and accumulate `delta` events into streamingDraft.
  // On `final`, both rows are authoritative — refetch the thread
  // and clear the draft.
  async function sendStreamed(content: string) {
    if (!activeId) return;
    setStreaming(true);
    setStreamError(null);
    setStreamingDraft('');
    try {
      await streamSSE<ChatUserStored | ChatDelta | ChatFinal | ChatErrorEvent>(
        `/v1/conversations/${activeId}/messages/stream`,
        { method: 'POST', body: { content } },
        (ev) => {
          switch (ev.type) {
            case 'user_stored': {
              const data = ev.data as ChatUserStored;
              // Optimistically inject the just-stored user message
              // into the cached conversation so it appears before
              // the assistant deltas start arriving.
              qc.setQueryData<GetConv>(['conversation', activeId], (prev) => {
                if (!prev) return prev;
                if (prev.messages.some((m) => m.id === data.message.id)) return prev;
                return { ...prev, messages: [...prev.messages, data.message] };
              });
              break;
            }
            case 'delta': {
              const data = ev.data as ChatDelta;
              setStreamingDraft((s) => s + data.text);
              break;
            }
            case 'final': {
              const data = ev.data as ChatFinal;
              setStreamingDraft('');
              qc.setQueryData<GetConv>(['conversation', activeId], (prev) => {
                if (!prev) return prev;
                // Replace any optimistic user copy and append the
                // assistant message. This is idempotent if the server
                // already echoed user_stored.
                const others = prev.messages.filter(
                  (m) => m.id !== data.user_message.id && m.id !== data.assistant_message.id,
                );
                return {
                  ...prev,
                  messages: [...others, data.user_message, data.assistant_message],
                };
              });
              qc.invalidateQueries({ queryKey: ['conversations'] });
              break;
            }
            case 'error': {
              const data = ev.data as ChatErrorEvent;
              setStreamError(data.message ?? 'stream error');
              break;
            }
          }
        },
      );
      setDraft('');
    } catch (err) {
      setStreamError(err instanceof ApiError ? `${err.status}: ${err.message}` : (err as Error).message);
    } finally {
      setStreaming(false);
    }
  }

  // Auto-scroll on new messages — pinned tail is the expected chat UX
  // and we don't have message virtualization yet. Also reacts to the
  // streaming draft growing so the in-progress reply stays visible.
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [conversation.data?.messages.length, streamingDraft]);

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!draft.trim() || !activeId || streaming) return;
    void sendStreamed(draft);
  }

  return (
    <main>
      <h1>Chat</h1>

      <div className="row" style={{ alignItems: 'flex-start', gap: '1rem' }}>
        <aside style={{ width: 280, flexShrink: 0 }}>
          <button
            className="primary"
            onClick={() => startConv.mutate()}
            disabled={startConv.isPending}
            style={{ width: '100%', marginBottom: '0.5rem' }}
          >
            {startConv.isPending ? 'Создаём…' : '+ Новый тред'}
          </button>

          {conversations.isLoading && <p className="muted">Загрузка…</p>}
          {conversations.error && <p className="error">{humanize(conversations.error)}</p>}

          <ul className="list">
            {conversations.data?.items.map((c) => (
              <li key={c.id}>
                <button
                  onClick={() => setActiveId(c.id)}
                  style={{
                    width: '100%',
                    textAlign: 'left',
                    fontWeight: c.id === activeId ? 600 : 400,
                  }}
                >
                  {c.title ?? `Тред ${c.id.slice(0, 8)}`}
                  <div className="muted" style={{ fontSize: '0.75rem' }}>
                    {new Date(c.updated_at).toLocaleString()}
                  </div>
                </button>
              </li>
            ))}
            {conversations.data?.items.length === 0 && (
              <li className="muted">Нет тредов — создай новый.</li>
            )}
          </ul>
        </aside>

        <section style={{ flex: 1, minWidth: 0 }}>
          {!activeId && <p className="muted">Выбери тред слева или создай новый.</p>}

          {activeId && conversation.isLoading && <p className="muted">Загрузка…</p>}
          {conversation.error && <p className="error">{humanize(conversation.error)}</p>}

          {conversation.data && (
            <>
              <div
                style={{
                  border: '1px solid #e5e5e5',
                  borderRadius: 6,
                  padding: '0.75rem',
                  height: 480,
                  overflowY: 'auto',
                  background: '#fff',
                }}
              >
                {conversation.data.messages.length === 0 && (
                  <p className="muted">Пусто. Напиши первое сообщение.</p>
                )}
                {conversation.data.messages.map((m) => (
                  <div key={m.id} className={`message ${m.role}`}>
                    <div className="role">{m.role}</div>
                    <div>{m.content}</div>
                  </div>
                ))}
                {streamingDraft && (
                  <div className="message assistant" style={{ opacity: 0.85 }}>
                    <div className="role">assistant…</div>
                    <div>{streamingDraft}</div>
                  </div>
                )}
                <div ref={messagesEndRef} />
              </div>

              <form onSubmit={onSubmit} className="col" style={{ marginTop: '0.5rem' }}>
                <textarea
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder="Что у тебя в голове?"
                  disabled={streaming}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                      e.preventDefault();
                      onSubmit(e as unknown as React.FormEvent);
                    }
                  }}
                />
                <div className="row">
                  <button
                    type="submit"
                    className="primary"
                    disabled={streaming || !draft.trim()}
                  >
                    {streaming ? 'Отправка…' : 'Отправить (⌘/Ctrl+Enter)'}
                  </button>
                  {streamError && <span className="error">{streamError}</span>}
                </div>
              </form>
            </>
          )}
        </section>
      </div>
    </main>
  );
}

function humanize(err: unknown): string {
  if (err instanceof ApiError) return `${err.status}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return 'Неизвестная ошибка';
}
