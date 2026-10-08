// Shapes of the data the server sends. They mirror the Go types in
// internal/server and internal/comments.

export type Author = { kind: 'human' | 'agent' | ''; name: string };

export type Message = {
  id: string;
  thread_id: string;
  author: Author;
  text: string;
  html: string;
  created_at: string;
  edited_at?: string;
};

export type ElementInfo = {
  tag: string;
  label?: string;
  selector?: string;
  before?: string;
  after?: string;
};

export type Anchor = {
  rev?: string;
  start: number;
  end: number;
  quote?: string;
  display?: string;
  prefix?: string;
  suffix?: string;
  section?: string;
  state?: string;
  element?: ElementInfo;
};

export type Segment = { key: string; start: number; end: number; text: string };

export type LocationState = 'ok' | 'changed' | 'deleted' | 'unplaced' | 'page' | 'dom';

export type Thread = {
  id: string;
  doc_id: string;
  scope: 'text' | 'element' | 'page';
  anchor: Anchor;
  status: 'open' | 'resolved';
  created_at: string;
  resolved_at?: string;
  resolved_by?: Author;
  updated_at: string;
  messages: Message[];
  awaiting: '' | 'human' | 'agent';
  location: {
    state: LocationState;
    line_start?: number;
    line_end?: number;
    current_text?: string;
    rendered_text?: string;
    section?: string;
  };
  placement: { segments?: Segment[]; element?: string; point?: Segment };
};

export type Heading = { level: number; text: string; id: string; start: number; path: string };

export type DocData = {
  kind: 'markdown' | 'code' | 'text' | 'html' | 'pdf' | 'image' | 'binary';
  rev: string;
  title: string;
  html?: string;
  tops?: { key: string; src: string }[];
  has_mermaid?: boolean;
  is_marp?: boolean;
  editable?: boolean;
  lang?: string;
  modified?: string;
  size: number;
  headings?: Heading[];
  raw: string;
  embed?: string;
  frontmatter?: boolean;
};

export type Entry = { name: string; path: string; url: string; dir?: boolean; threads?: number };

export type Crumb = { name: string; url: string; opened: boolean };

export type RootInfo = { path: string; url: string; name: string; display: string };

export type HomeData = {
  folders: { path: string; url: string; display: string; name: string; exists: boolean; used: string }[];
  recent: { path: string; url: string; display: string; name: string; open: number; awaiting: number; updated: string }[];
  inbox: { thread_id: string; path: string; url: string; display: string; quote?: string; scope: string; last: Message; count: number }[];
};

export type PageData = {
  view: 'doc' | 'dir' | 'home' | 'notfound' | 'closed' | 'message';
  version: string;
  assets: string;
  user: string;
  role: 'owner' | 'guest';
  path?: string;
  url?: string;
  display?: string;
  name?: string;
  root?: RootInfo;
  crumbs?: Crumb[];
  doc?: DocData;
  threads: Thread[];
  cursor: number;
  entries?: Entry[];
  readme?: DocData;
  suggestions?: { path: string; url: string; display: string; reason: string }[];
  nearest?: string;
  nearest_url?: string;
  relink?: { id: string; path: string; display: string; threads: number }[];
  home?: HomeData;
  content_origin?: string;
  message?: string;
  embedded?: boolean; // shown in another app's frame: the document and its comments only
};

export type SelectionPayload = {
  start_key: string;
  start_offset: number;
  end_key: string;
  end_offset: number;
  quote: string;
  prefix?: string;
  suffix?: string;
};

export type ElementPayload = {
  key: string;
  element: ElementInfo;
  range?: SelectionPayload;
};

// What a new comment is being made on, before it exists.
export type Draft =
  | { kind: 'text'; selection: SelectionPayload; top: number }
  | { kind: 'element'; element: ElementPayload; label: string; top: number }
  | { kind: 'page'; top: number };
