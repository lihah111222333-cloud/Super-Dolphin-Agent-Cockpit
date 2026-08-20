__attribute__((objc_root_class))
@interface Calculator
- (int)add:(int)left to:(int)right;
@end

@implementation Calculator
- (int)add:(int)left to:(int)right {
  return left + right;
}
@end
