// Shared pagination slice. A page reducer keeps `pagination` in its state and
// forwards `pagination/*` actions here; rules that span slices (e.g. "a filter change
// returns to the first page") stay in the page reducer, using `toFirstPage`.

export type Pagination = {
  page: number;
  size: number;
  /** 0 or 1, matching the API the page talks to. */
  firstPage: 0 | 1;
};

export type PaginationAction =
  | { type: "pagination/pageChanged"; page: number }
  | { type: "pagination/sizeChanged"; size: number };

export function createPagination(size: number, firstPage: 0 | 1 = 0): Pagination {
  return { page: firstPage, size, firstPage };
}

export function isPaginationAction(action: { type: string }): action is PaginationAction {
  return action.type.startsWith("pagination/");
}

/** The same pagination on its first page (same object when already there). */
export function toFirstPage(pagination: Pagination): Pagination {
  return pagination.page === pagination.firstPage ? pagination : { ...pagination, page: pagination.firstPage };
}

/** Index of the last page for `total` items (never below the first page). */
export function lastPage(pagination: Pagination, total: number): number {
  return Math.max(pagination.firstPage, Math.ceil(total / pagination.size) - 1 + pagination.firstPage);
}

export function paginationReducer(state: Pagination, action: PaginationAction): Pagination {
  switch (action.type) {
    case "pagination/pageChanged": {
      const page = Math.max(state.firstPage, action.page);
      return page === state.page ? state : { ...state, page };
    }
    case "pagination/sizeChanged":
      if (action.size === state.size) return state;
      return { ...state, page: state.firstPage, size: action.size };
  }
}
