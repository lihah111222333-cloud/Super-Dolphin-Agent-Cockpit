export const Call = {
  async ByID() {
    throw new Error('runtime mock: unavailable in browser test');
  },
};

export const Events = {
  On() {
    return () => {};
  },
  Off() {},
};
