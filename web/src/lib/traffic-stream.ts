import type { TrafficStreamDirection, TrafficStreamMessage, TrafficStreamPage } from "./types";

export interface TrafficStreamState {
  items: TrafficStreamMessage[];
  total: number;
  messageTotal: number;
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
}

export function emptyTrafficStreamState(): TrafficStreamState {
  return { items: [], total: 0, messageTotal: 0, loading: true, loadingMore: false, error: null };
}

type FetchPage = (
  id: string,
  offset: number,
  limit: number,
  direction: TrafficStreamDirection,
) => Promise<TrafficStreamPage>;

// Offsets count rows in the filtered result, not the original stream's seq.
// Every refresh/selection invalidates pending pages, including their errors.
export function createTrafficStreamPager(fetchPage: FetchPage, onChange: (state: TrafficStreamState) => void) {
  let state = emptyTrafficStreamState();
  let generation = 0;
  let exchangeId = "";
  let direction: TrafficStreamDirection = "";

  function update(patch: Partial<TrafficStreamState>) {
    state = { ...state, ...patch };
    onChange(state);
  }

  async function load(offset: number) {
    const gen = ++generation;
    update({
      ...(offset === 0 ? { items: [], total: 0, messageTotal: 0 } : {}),
      loading: offset === 0,
      loadingMore: offset > 0,
      error: null,
    });
    try {
      const page = await fetchPage(exchangeId, offset, 50, direction);
      if (gen !== generation) return;
      update({
        items: offset === 0 ? (page.items ?? []) : [...state.items, ...(page.items ?? [])],
        total: page.total ?? 0,
        messageTotal: page.message_total,
        loading: false,
        loadingMore: false,
      });
    } catch (error) {
      if (gen !== generation) return;
      update({ error: error instanceof Error ? error.message : String(error), loading: false, loadingMore: false });
    }
  }

  return {
    select(id: string, nextDirection: TrafficStreamDirection) {
      exchangeId = id;
      direction = nextDirection;
      void load(0);
    },
    refresh() {
      void load(0);
    },
    loadMore() {
      // Guard synchronously, before React renders a disabled Load more button.
      if (state.loading || state.loadingMore || state.items.length >= state.total) return;
      void load(state.items.length);
    },
    cancel() {
      ++generation;
    },
  };
}
