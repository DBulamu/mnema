import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError } from '../lib/api';

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
type SendMsg = { user_message: Message; assistant_message: Message };

export function ChatPage() {
  const qc = useQueryClient();
  const [activeId, setActiveId] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
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

  const sendMsg = useMutation({
    mutationFn: (content: string) =>
      api<SendMsg>(`/v1/conversations/${activeId}/messages`, {
        method: 'POST',
        body: { content },
      }),
    onSuccess: () => {
      setDraft('');
      qc.invalidateQueries({ queryKey: ['conversation', activeId] });
      qc.invalidateQueries({ queryKey: ['conversations'] });
    },
  });

  // Auto-scroll on new messages — pinned tail is the expected chat UX
  // and we don't have message virtualization yet.
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [conversation.data?.messages.length]);

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!draft.trim() || !activeId) return;
    sendMsg.mutate(draft);
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
                <div ref={messagesEndRef} />
              </div>

              <form onSubmit={onSubmit} className="col" style={{ marginTop: '0.5rem' }}>
                <textarea
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder="Что у тебя в голове?"
                  disabled={sendMsg.isPending}
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
                    disabled={sendMsg.isPending || !draft.trim()}
                  >
                    {sendMsg.isPending ? 'Отправка…' : 'Отправить (⌘/Ctrl+Enter)'}
                  </button>
                  {sendMsg.error && <span className="error">{humanize(sendMsg.error)}</span>}
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
