// The library page's search input, registered so the mobile tab bar can focus
// it synchronously inside the tap handler. iOS only opens the keyboard for a
// focus() that happens during the user gesture; a focus after an async
// navigation lands silently, which is why the tab bar tries this first.

class SearchBox {
	el: HTMLInputElement | null = null;

	/** Focus the registered input; false when the library page is not mounted. */
	focus(): boolean {
		const el = this.el;
		if (!el) return false;
		el.focus();
		el.scrollIntoView?.({ block: 'center' });
		return true;
	}
}

export const searchBox = new SearchBox();
